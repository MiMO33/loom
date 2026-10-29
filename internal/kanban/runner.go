package kanban

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

func EmitEvent(ev Event) {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.At == "" {
		ev.At = NowISO()
	}
	if ev.TaskID == "" {
		ev.TaskID = os.Getenv("AGENT_KANBAN_TASK_ID")
	}
	if ev.SessionID == "" {
		ev.SessionID = os.Getenv("AGENT_KANBAN_SESSION_ID")
	}
	if ev.RunID == "" {
		ev.RunID = os.Getenv("AGENT_KANBAN_RUN_ID")
	}

	b, _ := json.Marshal(ev)
	if eventsPath := os.Getenv("AGENT_KANBAN_EVENTS"); eventsPath != "" {
		if f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}

	baseURL := os.Getenv("AGENT_KANBAN_URL")
	if baseURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/hooks", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("AGENT_KANBAN_TOKEN"))
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// RunRunner executes a LaunchSpec JSON file (`loom runner <spec.json>`).
func RunRunner(specPath string) int {
	b, err := os.ReadFile(specPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var spec LaunchSpec
	if err := json.Unmarshal(b, &spec); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if spec.Cwd != "" {
		_ = os.Chdir(spec.Cwd)
		_ = os.Setenv("PWD", spec.Cwd)
	}
	for k, v := range spec.Env {
		_ = os.Setenv(k, v)
	}
	_ = os.Unsetenv("CLAUDECODE")

	if spec.Mode == ModeTmux {
		time.Sleep(200 * time.Millisecond)
	}

	var (
		mu          sync.Mutex
		nativeID    string
		finalText   string
		lastMessage string
		failed      bool
	)

	assistant := func(text string) {
		if text == "" {
			return
		}
		mu.Lock()
		if text == lastMessage {
			mu.Unlock()
			return
		}
		finalText = text
		lastMessage = text
		if len(text) > 100000 {
			text = text[len(text)-100000:]
		}
		mu.Unlock()
		EmitEvent(Event{Type: "message", Text: text})
	}

	parseLine := func(line string) {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return
		}
		typ, _ := raw["type"].(string)

		var id string
		if tid, ok := raw["thread_id"].(string); ok && tid != "" {
			id = tid
		} else if sid, ok := raw["session_id"].(string); ok && sid != "" {
			id = sid
		} else if sid, ok := raw["sessionID"].(string); ok && sid != "" {
			id = sid
		} else if typ == "session" {
			id, _ = raw["id"].(string)
		}
		if id != "" {
			mu.Lock()
			changed := id != nativeID
			if changed {
				nativeID = id
			}
			mu.Unlock()
			if changed {
				EmitEvent(Event{Type: "identity", NativeID: id})
			}
		}

		if typ == "item.completed" {
			if item, ok := raw["item"].(map[string]any); ok && item["type"] == "agent_message" {
				if t, ok := item["text"].(string); ok {
					assistant(t)
				}
			}
		}
		if typ == "result" {
			if r, ok := raw["result"].(string); ok && r != "" {
				assistant(r)
			}
			// Mid-turn permission_denials do not mean the overall task failed;
			// rely on is_error and error subtypes for final turn status.
			isErr, _ := raw["is_error"].(bool)
			subtype, _ := raw["subtype"].(string)
			if isErr || strings.HasPrefix(subtype, "error") {
				mu.Lock()
				failed = true
				mu.Unlock()
			}
		}
		if typ == "turn.failed" || typ == "error" {
			mu.Lock()
			failed = true
			mu.Unlock()
		}
		if typ == "assistant" {
			if msg, ok := raw["message"].(map[string]any); ok {
				assistant(extractContentText(msg["content"]))
			}
		}
		if typ == "message" {
			if role, _ := raw["role"].(string); role == "assistant" {
				assistant(extractContentText(raw["content"]))
			}
		}
		if typ == "text" {
			if part, ok := raw["part"].(map[string]any); ok {
				if t, ok := part["text"].(string); ok {
					assistant(t)
				}
			}
		}
		if typ == "message_end" {
			if msg, ok := raw["message"].(map[string]any); ok && msg["role"] == "assistant" {
				assistant(extractContentText(msg["content"]))
				stopReason, _ := msg["stopReason"].(string)
				errMsg, _ := msg["errorMessage"].(string)
				if stopReason == "error" || errMsg != "" {
					mu.Lock()
					failed = true
					mu.Unlock()
				}
			}
		}
	}

	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = spec.Cwd
	cmd.Env = os.Environ()

	var logFile *os.File
	var wg sync.WaitGroup

	if spec.Mode == ModeHeadless {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdin = strings.NewReader(spec.Stdin)
		logFile, _ = os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()

		if stdout != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var pending bytes.Buffer
				buf := make([]byte, 32*1024)
				for {
					n, err := stdout.Read(buf)
					if n > 0 {
						chunk := buf[:n]
						if logFile != nil {
							_, _ = logFile.Write(chunk)
						}
						pending.Write(chunk)
						for {
							data := pending.Bytes()
							idx := bytes.IndexByte(data, '\n')
							if idx < 0 {
								break
							}
							line := string(data[:idx])
							pending.Next(idx + 1)
							parseLine(line)
						}
						if pending.Len() > 4*1024*1024 {
							pending.Reset()
						}
					}
					if err != nil {
						if pending.Len() > 0 {
							parseLine(pending.String())
						}
						break
					}
				}
			}()
		}
		if stderr != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if logFile != nil {
					_, _ = io.Copy(logFile, stderr)
				} else {
					_, _ = io.Copy(io.Discard, stderr)
				}
			}()
		}
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		sig, ok := <-sigCh
		if !ok || cmd.Process == nil {
			return
		}
		sysSig, _ := sig.(syscall.Signal)
		target := cmd.Process.Pid
		if spec.Mode == ModeHeadless {
			target = -target
		}
		_ = syscall.Kill(target, sysSig)
		time.Sleep(2500 * time.Millisecond)
		_ = syscall.Kill(target, syscall.SIGKILL)
	}()

	var startErr error
	if err := cmd.Start(); err != nil {
		startErr = err
	}

	var waitErr error
	if startErr == nil {
		wg.Wait()
		waitErr = cmd.Wait()
	}
	signal.Stop(sigCh)
	close(sigCh)
	if logFile != nil {
		_ = logFile.Close()
	}

	mu.Lock()
	exitCode := 0
	var errText string
	if startErr != nil {
		exitCode = 1
		errText = startErr.Error()
	} else if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				exitCode = 1
				errText = "进程收到 " + status.Signal().String()
			} else {
				exitCode = exitErr.ExitCode()
				if exitCode == 0 {
					exitCode = 1
				}
			}
		} else {
			exitCode = 1
			errText = waitErr.Error()
		}
	}
	if failed && exitCode == 0 {
		exitCode = 1
	}
	if errText == "" && failed {
		errText = "Agent 报告执行失败"
	}

	if finalText != "" {
		_ = os.WriteFile(spec.FinalPath, []byte(finalText), 0o600)
	}
	resObj := map[string]any{
		"runId":    spec.RunID,
		"exitCode": exitCode,
	}
	if nativeID != "" {
		resObj["nativeId"] = nativeID
	}
	if errText != "" {
		resObj["error"] = errText
	}
	resBytes, _ := json.Marshal(resObj)
	_ = os.WriteFile(spec.ResultPath+".tmp", resBytes, 0o600)
	_ = os.Rename(spec.ResultPath+".tmp", spec.ResultPath)
	nat := nativeID
	mu.Unlock()

	EmitEvent(Event{
		Type:     "exit",
		RunID:    spec.RunID,
		ExitCode: &exitCode,
		NativeID: nat,
		Error:    errText,
	})
	return exitCode
}

func extractContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok && m["type"] == "text" {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// RunHook processes an agent hook callback (`loom hook [EventName]`).
func RunHook(args []string) int {
	input, _ := io.ReadAll(os.Stdin)
	var data map[string]any
	_ = json.Unmarshal(input, &data)

	var name string
	if len(args) > 0 {
		name = args[0]
	} else if n, ok := data["hook_event_name"].(string); ok {
		name = n
	}

	aliases := map[string]string{
		"SessionStart":       "identity",
		"UserPromptSubmit":   "running",
		"PreToolUse":         "running",
		"PostToolUse":        "running",
		"PostToolUseFailure": "running",
		"Stop":               "awaiting",
		"StopFailure":        "awaiting",
		"Interrupt":          "awaiting",
		"PermissionRequest":  "awaiting",
		"Notification":       "awaiting",
		"SessionEnd":         "session_end",
	}

	text, _ := data["last_assistant_message"].(string)
	if text == "" && name == "Stop" {
		if tp, ok := data["transcript_path"].(string); ok && tp != "" {
			text = readTranscriptTail(tp)
		}
	}
	if len(text) > 100000 {
		text = text[len(text)-100000:]
	}

	evType := aliases[name]
	if evType == "" {
		evType = name
	}
	nativeID, _ := data["session_id"].(string)
	if nativeID == "" {
		nativeID, _ = data["thread-id"].(string)
	}
	var errMsg string
	if name == "StopFailure" {
		errMsg = "Agent 本轮执行失败，请查看日志"
	}

	EmitEvent(Event{
		Type:     evType,
		NativeID: nativeID,
		Text:     text,
		Error:    errMsg,
	})
	return 0
}

func readTranscriptTail(path string) string {
	b := TailBytes(path, 128*1024)
	if len(b) == 0 {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var item map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &item); err != nil {
			continue
		}
		if msg, ok := item["message"].(map[string]any); ok && msg["role"] == "assistant" {
			if t := extractContentText(msg["content"]); t != "" {
				return t
			}
		}
	}
	return ""
}

// RunCLICommand handles `loom output <paths...>` and `loom status <running|awaiting>`.
func RunCLICommand(cmd string, args []string) int {
	if os.Getenv("AGENT_KANBAN_TASK_ID") == "" || os.Getenv("AGENT_KANBAN_EVENTS") == "" {
		fmt.Fprintln(os.Stderr, "请在看板启动的 Agent 环境中使用此 CLI。")
		return 1
	}
	switch {
	case cmd == "output" && len(args) > 0:
		var resolved []string
		for _, p := range args {
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			resolved = append(resolved, abs)
		}
		EmitEvent(Event{Type: "output", Paths: resolved})
		fmt.Printf("已回报 %d 个输出文件\n", len(resolved))
		return 0
	case cmd == "status" && len(args) > 0 && (args[0] == "running" || args[0] == "awaiting"):
		EmitEvent(Event{Type: args[0]})
		return 0
	default:
		fmt.Fprintln(os.Stderr, "用法：loom output <文件路径...> | status running|awaiting")
		return 1
	}
}

// RunFixtureAgent is a built-in mock agent used in integration tests.
func RunFixtureAgent(args []string) int {
	for _, a := range args {
		if a == "--version" {
			fmt.Println("fixture-agent 1.0")
			return 0
		}
	}
	self := SelfBin()
	hook := func(name string, extra map[string]any) {
		payload := map[string]any{"session_id": os.Getenv("AGENT_KANBAN_SESSION_ID")}
		for k, v := range extra {
			payload[k] = v
		}
		b, _ := json.Marshal(payload)
		cmd := exec.Command(self, "hook", name)
		cmd.Stdin = bytes.NewReader(b)
		cmd.Env = os.Environ()
		_ = cmd.Run()
	}

	var prompt string
	if len(args) > 0 {
		prompt = args[len(args)-1]
	}
	if strings.HasPrefix(prompt, "@") {
		if b, err := os.ReadFile(prompt[1:]); err == nil {
			prompt = string(b)
		}
	}
	st, err := os.Stdin.Stat()
	isTTY := err == nil && (st.Mode()&os.ModeCharDevice) != 0
	if !isTTY {
		if b, err := io.ReadAll(os.Stdin); err == nil && len(b) > 0 {
			prompt = string(b)
		}
	}

	archive := strings.Contains(prompt, "只输出笔记正文")
	sid := os.Getenv("AGENT_KANBAN_SESSION_ID")
	if sid == "" {
		sid = uuid.NewString()
	}
	b, _ := json.Marshal(map[string]any{"type": "thread.started", "thread_id": sid})
	fmt.Println(string(b))
	hook("SessionStart", nil)
	hook("UserPromptSubmit", nil)

	if strings.Contains(prompt, "[slow]") {
		time.Sleep(1600 * time.Millisecond)
	} else {
		time.Sleep(120 * time.Millisecond)
	}

	if strings.Contains(prompt, "[fail]") && !archive {
		fmt.Fprintln(os.Stderr, "模拟 Agent 执行失败")
		return 2
	}
	if !archive && strings.Contains(prompt, "[write]") {
		_ = os.WriteFile("result.md", []byte("# 已完成的报告\n\n测试产物。\n"), 0o644)
		cwd, _ := os.Getwd()
		cmd := exec.Command(self, "output", filepath.Join(cwd, "result.md"))
		cmd.Env = os.Environ()
		_ = cmd.Run()
		if !strings.Contains(prompt, "[no-commit]") {
			tid := os.Getenv("AGENT_KANBAN_TASK_ID")
			_, _ = Git(cwd, "add", "--", "result.md")
			_, _ = Git(cwd, "commit", "--only", "-qm", fmt.Sprintf("feat: fixture commit\n\nLoom-Task: %s", tid), "--", "result.md")
		}
	}

	text := "执行完成，已检查结果。"
	if archive {
		text = "# 任务归档\n\n## 结果\n\n已生成报告和输出文件。\n\n## 过程与验证\n\n已检查结果，保留关键决策。"
	}
	resObj := map[string]any{
		"type":     "result",
		"subtype":  "success",
		"result":   text,
		"is_error": false,
	}
	if strings.Contains(prompt, "[denial]") {
		resObj["permission_denials"] = []map[string]any{
			{"tool_name": "Bash", "tool_use_id": "call_1"},
		}
	}
	resJSON, _ := json.Marshal(resObj)
	fmt.Println(string(resJSON))
	hook("Stop", map[string]any{"last_assistant_message": text})

	if isTTY {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := scanner.Text()
			hook("UserPromptSubmit", nil)
			time.Sleep(250 * time.Millisecond)
			reply := "收到：" + line
			fmt.Println(reply)
			hook("Stop", map[string]any{"last_assistant_message": reply})
		}
	}
	return 0
}
