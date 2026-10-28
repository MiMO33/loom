package kanban

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Executables map[Tool]string

func Quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func SelfBin() string {
	exe, err := os.Executable()
	if err != nil {
		return "loom"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

type LaunchOptions struct {
	Session    *Session
	Prompt     string
	Cwd        string
	TaskDir    string
	RunDir     string
	AppRoot    string
	Executable string
}

type CommandSpec struct {
	Command string
	Args    []string
	Stdin   string
}

const piExtensionTemplate = `import { execFile } from 'node:child_process';

const bin = process.env.LOOM_BIN || %q;

export default function (pi) {
  function report(type, nativeId) {
    return new Promise(resolve => {
      const child = execFile(bin, ['hook', type], { timeout: 2000 }, () => resolve());
      child.stdin.end(JSON.stringify({ session_id: nativeId }));
    });
  }

  pi.on('session_start', (_event, ctx) => report('SessionStart', ctx.sessionManager.getSessionFile()));
  pi.on('before_agent_start', () => report('UserPromptSubmit'));
  pi.on('agent_settled', () => report('Stop'));
  pi.on('session_shutdown', () => report('SessionEnd'));
}
`

func DefaultCommandForTool(t Tool) string {
	switch t {
	case ToolCursor:
		if _, err := exec.LookPath("cursor-agent"); err == nil {
			return "cursor-agent"
		}
		return "cursor"
	default:
		return string(t)
	}
}

func CommandFor(opts LaunchOptions) CommandSpec {
	self := SelfBin()
	hookCmd := Quote(self) + " hook"
	events := []string{"SessionStart", "UserPromptSubmit", "Stop", "PermissionRequest", "PostToolUse", "SessionEnd"}

	var args []string
	var stdin string
	s := opts.Session

	switch s.Tool {
	case ToolClaude:
		settingsPath := filepath.Join(opts.RunDir, "claude-settings.json")
		hookMap := make(map[string]any)
		for _, ev := range append(events, "StopFailure") {
			hookMap[ev] = []map[string]any{
				{
					"hooks": []map[string]any{
						{"type": "command", "command": hookCmd + " " + ev, "timeout": 2},
					},
				},
			}
		}
		settings := map[string]any{"hooks": hookMap}
		if s.Mode == ModeHeadless {
			// Headless runs cannot answer interactive permission prompts (--permission-prompts none),
			// so explicitly allow standard coding & verification tools in session settings.
			settings["permissions"] = map[string]any{
				"allow": []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep"},
			}
		}
		b, _ := json.Marshal(settings)
		_ = os.WriteFile(settingsPath, b, 0o600)

		args = []string{"--settings", settingsPath, "--add-dir", opts.TaskDir}
		if opts.Cwd != "" && opts.Cwd != opts.TaskDir {
			args = append(args, "--add-dir", opts.Cwd)
		}
		if s.NativeID != "" {
			args = append(args, "--resume", s.NativeID)
		} else {
			args = append(args, "--session-id", s.ID)
		}
		if s.Mode == ModeHeadless {
			args = append(args, "--print", "--verbose", "--output-format", "stream-json", "--permission-mode", "acceptEdits", "--permission-prompts", "none")
			stdin = opts.Prompt
		} else {
			args = append(args, opts.Prompt)
		}

	case ToolCodex:
		approval := "on-request"
		if s.Mode == ModeHeadless {
			approval = "never"
		}
		args = []string{"-s", "workspace-write", "-a", approval, "--add-dir", opts.TaskDir}
		if opts.Cwd != "" && opts.Cwd != opts.TaskDir {
			args = append(args, "--add-dir", opts.Cwd)
		}
		for _, ev := range append(events, "Interrupt") {
			cmdJSON, _ := json.Marshal(hookCmd + " " + ev)
			args = append(args, "-c", fmt.Sprintf(`hooks.%s=[{hooks=[{type="command",command=%s,timeout=2}]}]`, ev, string(cmdJSON)))
		}
		if s.Mode == ModeHeadless {
			args = append(args, "exec")
			if s.NativeID != "" {
				args = append(args, "resume")
			}
			args = append(args, "--skip-git-repo-check", "--json")
			if s.NativeID != "" {
				args = append(args, s.NativeID)
			}
			args = append(args, "-")
			stdin = opts.Prompt
		} else {
			args = append(args, "--no-alt-screen")
			if s.NativeID != "" {
				args = append(args, "resume", s.NativeID)
			}
			args = append(args, opts.Prompt)
		}

	case ToolGemini:
		args = []string{"--include-directories", opts.TaskDir}
		if opts.Cwd != "" && opts.Cwd != opts.TaskDir {
			args = append(args, "--include-directories", opts.Cwd)
		}
		if s.NativeID != "" {
			args = append(args, "--resume", s.NativeID)
		}
		if s.Mode == ModeHeadless {
			args = append(args, "--approval-mode", "auto_edit", "--output-format", "stream-json", "-p", opts.Prompt)
		} else {
			args = append(args, "-i", opts.Prompt)
		}

	case ToolOpenCode:
		if s.Mode == ModeHeadless {
			args = []string{"run", "--format", "json"}
			if s.NativeID != "" {
				args = append(args, "--session", s.NativeID)
			}
			args = append(args, opts.Prompt)
		} else {
			if s.NativeID != "" {
				args = append(args, "--session", s.NativeID)
			}
			args = append(args, "--prompt", opts.Prompt)
		}

	case ToolCursor:
		if s.NativeID != "" {
			args = append(args, "--resume", s.NativeID)
		}
		if s.Mode == ModeHeadless {
			args = append(args, "--print", "--output-format", "stream-json", "--force", opts.Prompt)
		} else {
			args = append(args, opts.Prompt)
		}

	default: // ToolPi
		extPath := filepath.Join(opts.RunDir, "pi-extension.mjs")
		_ = os.WriteFile(extPath, []byte(fmt.Sprintf(piExtensionTemplate, self)), 0o600)
		args = []string{"--extension", extPath}
		if s.NativeID != "" {
			args = append(args, "--session", s.NativeID)
		} else {
			args = append(args, "--session-id", s.ID, "--session-dir", filepath.Join(opts.TaskDir, "pi-sessions"))
		}
		if s.Mode == ModeHeadless {
			args = append(args, "--mode", "json", "--print")
			stdin = opts.Prompt
		} else {
			args = append(args, "@"+filepath.Join(opts.RunDir, "prompt.md"))
		}
	}

	command := DefaultCommandForTool(s.Tool)
	if opts.Executable != "" {
		command = opts.Executable
	}
	return CommandSpec{Command: command, Args: args, Stdin: stdin}
}

func CheckToolHealth(command string) ToolHealth {
	if command == "cursor" {
		command = DefaultCommandForTool(ToolCursor)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	flag := "--version"
	if command == "tmux" {
		flag = "-V"
	}
	cmd := exec.CommandContext(ctx, command, flag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ToolHealth{Available: false, Version: "未安装或无法执行"}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "WARNING") {
			return ToolHealth{Available: true, Version: line}
		}
	}
	return ToolHealth{Available: true, Version: "已安装"}
}

func ValidateMode(oneShot bool, mode Mode) error {
	if oneShot && mode != ModeHeadless {
		return Fail("一次性任务使用 headless 模式；使用 tmux 请取消勾选一次性任务。", http.StatusBadRequest)
	}
	return nil
}
