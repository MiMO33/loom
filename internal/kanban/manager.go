package kanban

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type ManagerOptions struct {
	AppRoot     string
	DataDir     string
	URL         string
	Executables Executables
}

func Expand(p string) string {
	home, _ := os.UserHomeDir()
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func TailBytes(path string, limit int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	size := st.Size()
	if size == 0 {
		return nil
	}
	readLen := min(size, limit)
	buf := make([]byte, readLen)
	_, _ = f.ReadAt(buf, max(0, size-readLen))
	return buf
}

func Tail(path string, limit int64) string {
	return string(TailBytes(path, limit))
}

type Manager struct {
	Options    ManagerOptions
	Store      *Store
	mu         sync.Mutex
	ticking    bool
	completing map[string]bool
	launching  map[string]bool
	repoLocks  map[string]bool
	stopCh     chan struct{}
	stopOnce   sync.Once
}

func NewManager(opts ManagerOptions) (*Manager, error) {
	store, err := NewStore(opts.DataDir)
	if err != nil {
		return nil, err
	}
	return &Manager{
		Options:    opts,
		Store:      store,
		completing: make(map[string]bool),
		launching:  make(map[string]bool),
		repoLocks:  make(map[string]bool),
		stopCh:     make(chan struct{}),
	}, nil
}

func (m *Manager) Lock()   { m.mu.Lock() }
func (m *Manager) Unlock() { m.mu.Unlock() }

func (m *Manager) Start() {
	go func() {
		_ = m.Tick()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				_ = m.Tick()
			}
		}
	}()
}

func (m *Manager) Close() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

func (m *Manager) RunDir(task *Task, session *Session) string {
	return filepath.Join(m.Store.Root, "tasks", task.ID, "runtime", session.ID, session.RunID)
}

func (m *Manager) LogPath(task *Task, session *Session) string {
	return filepath.Join(m.Store.Root, "tasks", task.ID, "logs", session.ID+".log")
}

func parseTimeMs(iso string) int64 {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return t.UnixMilli()
}

func (m *Manager) transition(session *Session, status SessionStatus, at string) {
	if at == "" {
		at = NowISO()
	}
	if session.Status == status {
		return
	}
	if session.Status == SessionRunning {
		delta := parseTimeMs(at) - parseTimeMs(session.StateSince)
		if delta > 0 {
			session.ActiveMs += delta
		}
	}
	session.Status = status
	session.StateSince = at
	if status == SessionEnded {
		session.EndedAt = at
	} else {
		session.EndedAt = ""
	}
}

type DraftInput struct {
	Title       string
	Description string
	Tool        Tool
	Mode        Mode
	OneShot     bool
	ProjectDir  string
	ArchivePath string
}

func (m *Manager) Create(in DraftInput) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(in)
}

func (m *Manager) createLocked(in DraftInput) (*Task, error) {
	if err := ValidateMode(in.OneShot, in.Mode); err != nil {
		return nil, err
	}
	archivePath := strings.TrimSpace(in.ArchivePath)
	if archivePath == "" {
		archivePath = DefaultArchivePath
	}
	projectDir := strings.TrimSpace(in.ProjectDir)
	if projectDir != "" {
		projectDir = Expand(projectDir)
		if inside(m.Store.Root, projectDir) {
			projectDir = ""
		}
	}
	if projectDir == "" && m.Store.Data.LastProjectDir != "" {
		projectDir = Expand(m.Store.Data.LastProjectDir)
	}
	now := NowISO()
	task := &Task{
		ID:          uuid.NewString(),
		Title:       in.Title,
		Description: in.Description,
		Status:      StatusTodo,
		Tool:        in.Tool,
		Mode:        in.Mode,
		OneShot:     in.OneShot,
		ProjectDir:  projectDir,
		ArchivePath: archivePath,
		CreatedAt:   now,
		UpdatedAt:   now,
		Input:       []TaskFile{},
		Output:      []TaskFile{},
		Sessions:    []*Session{},
		Archive:     ArchiveState{Status: "idle"},
	}
	m.Store.Data.Tasks = append([]*Task{task}, m.Store.Data.Tasks...)
	if task.ProjectDir != "" {
		m.Store.RememberProjectDir(task.ProjectDir)
	}
	dir := filepath.Join(m.Store.Root, "tasks", task.ID)
	for _, sub := range []string{"input", "output", "logs", "runtime"} {
		_ = os.MkdirAll(filepath.Join(dir, sub), 0o700)
	}
	m.Store.Touch(task)
	return task, nil
}

type PatchInput struct {
	Title       *string
	Description *string
	Tool        *Tool
	Mode        *Mode
	OneShot     *bool
	ProjectDir  *string
	ArchivePath *string
	Archived    *bool
}

func (m *Manager) Edit(task *Task, in PatchInput) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if in.ArchivePath != nil {
		trimmed := strings.TrimSpace(*in.ArchivePath)
		if trimmed == "" {
			trimmed = DefaultArchivePath
		}
		in.ArchivePath = &trimmed
	}
	if m.completing[task.ID] || m.launching[task.ID] {
		return nil, Fail("任务正在启动或完成，请稍后再编辑。", http.StatusConflict)
	}
	if task.Status == StatusDoing {
		return nil, Fail("请等待 Agent 停止后再编辑任务。", http.StatusConflict)
	}
	if task.Status == StatusDone {
		if in.Archived != nil {
			task.Archived = *in.Archived
		}
		hasOtherFields := in.Title != nil || in.Description != nil || in.Tool != nil || in.Mode != nil || in.OneShot != nil || in.ProjectDir != nil || in.ArchivePath != nil
		if hasOtherFields {
			if task.Archive.Status != "error" {
				return nil, Fail("任务已归档。", http.StatusConflict)
			}
			if in.ArchivePath != nil {
				task.ArchivePath = *in.ArchivePath
			}
		}
	} else {
		if in.Archived != nil {
			task.Archived = *in.Archived
		}
		oneShot := task.OneShot
		if in.OneShot != nil {
			oneShot = *in.OneShot
		}
		mode := task.Mode
		if in.Mode != nil {
			mode = *in.Mode
		}
		if err := ValidateMode(oneShot, mode); err != nil {
			return nil, err
		}
		if in.Title != nil {
			task.Title = *in.Title
		}
		if in.Description != nil {
			task.Description = *in.Description
		}
		if in.Tool != nil {
			task.Tool = *in.Tool
		}
		if in.Mode != nil {
			task.Mode = *in.Mode
		}
		if in.OneShot != nil {
			task.OneShot = *in.OneShot
		}
		if in.ProjectDir != nil {
			nextDir := strings.TrimSpace(*in.ProjectDir)
			if nextDir != "" {
				nextDir = Expand(nextDir)
				if inside(m.Store.Root, nextDir) {
					return nil, Fail("项目目录不能位于 .loom 数据目录内。", http.StatusBadRequest)
				}
			}
			if nextDir != task.ProjectDir {
				task.ProjectDir = nextDir
				task.Baseline = nil
				for _, s := range task.Sessions {
					if s.Kind == "work" {
						s.NativeID = ""
					}
				}
			}
			if task.ProjectDir != "" {
				m.Store.RememberProjectDir(task.ProjectDir)
			}
		}
		if in.ArchivePath != nil {
			task.ArchivePath = *in.ArchivePath
		}
	}
	m.Store.Touch(task)
	return task, nil
}

func (m *Manager) Delete(task *Task) error {
	m.mu.Lock()
	if m.completing[task.ID] || m.launching[task.ID] {
		m.mu.Unlock()
		return Fail("任务正在启动或完成，请稍后再删除。", http.StatusConflict)
	}
	for _, s := range task.Sessions {
		if s.Status != SessionEnded {
			_ = m.stopLocked(task, s)
		}
	}
	filtered := make([]*Task, 0, len(m.Store.Data.Tasks))
	for _, item := range m.Store.Data.Tasks {
		if item.ID != task.ID {
			filtered = append(filtered, item)
		}
	}
	m.Store.Data.Tasks = filtered
	m.Store.Save()
	taskDir := filepath.Join(m.Store.Root, "tasks", task.ID)
	archiveCopy := filepath.Join(m.Store.Root, "archive", task.ID+".md")
	m.mu.Unlock()

	_ = os.RemoveAll(taskDir)
	_ = os.Remove(archiveCopy)
	return nil
}

func (m *Manager) workingDir(task *Task) string {
	if task.ProjectDir != "" && !inside(m.Store.Root, Expand(task.ProjectDir)) {
		return Expand(task.ProjectDir)
	}
	if m.Store.Data.LastProjectDir != "" && !inside(m.Store.Root, Expand(m.Store.Data.LastProjectDir)) {
		return Expand(m.Store.Data.LastProjectDir)
	}
	if m.Options.AppRoot != "" && m.Options.AppRoot != "/" && !inside(m.Store.Root, Expand(m.Options.AppRoot)) {
		return Expand(m.Options.AppRoot)
	}
	return filepath.Join(m.Store.Root, "tasks", task.ID, "output")
}

func (m *Manager) prepareLocked(task *Task, tool Tool, mode Mode) (string, error) {
	if task.Status == StatusDone {
		return "", Fail("任务已完成。", http.StatusConflict)
	}
	if m.completing[task.ID] || task.Completion != nil {
		return "", Fail("任务正在执行完成流程。", http.StatusConflict)
	}
	task.ArchivePath = strings.TrimSpace(task.ArchivePath)
	if task.ArchivePath == "" {
		task.ArchivePath = DefaultArchivePath
	}
	if task.ProjectDir == "" || inside(m.Store.Root, Expand(task.ProjectDir)) {
		if m.Store.Data.LastProjectDir != "" && !inside(m.Store.Root, Expand(m.Store.Data.LastProjectDir)) {
			task.ProjectDir = Expand(m.Store.Data.LastProjectDir)
		} else if m.Options.AppRoot != "" && m.Options.AppRoot != "/" && !inside(m.Store.Root, Expand(m.Options.AppRoot)) {
			task.ProjectDir = Expand(m.Options.AppRoot)
		}
	}
	if task.ProjectDir == "" || inside(m.Store.Root, Expand(task.ProjectDir)) {
		return "", Fail("请先选择项目目录。", http.StatusBadRequest)
	}
	cwd := Expand(task.ProjectDir)
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "", Fail("项目目录不存在："+cwd, http.StatusBadRequest)
	}
	m.Store.RememberProjectDir(cwd)
	exe := DefaultCommandForTool(tool)
	if custom := m.Options.Executables[tool]; custom != "" {
		exe = custom
	}
	if !CheckToolHealth(exe).Available {
		return "", Fail(string(tool)+" 无法执行，请检查安装和 PATH。", http.StatusBadRequest)
	}
	if mode == ModeTmux && !CheckToolHealth("tmux").Available {
		return "", Fail("请先安装 tmux。", http.StatusBadRequest)
	}
	hasWork := false
	for _, s := range task.Sessions {
		if s.Kind == "work" {
			hasWork = true
			break
		}
	}
	if !hasWork || task.Baseline == nil {
		task.Baseline = Snapshot(cwd)
	}
	if task.Baseline != nil && m.repoLocks[task.Baseline.Root] {
		return "", Fail("项目正在提交，请稍后启动。", http.StatusConflict)
	}
	return cwd, nil
}

func (m *Manager) buildPrompt(task *Task, session *Session) string {
	desc := task.Description
	for _, f := range task.Input {
		desc = strings.ReplaceAll(desc, fmt.Sprintf("/api/tasks/%s/files/%s", task.ID, f.ID), f.Path)
	}
	var inputs []string
	for _, f := range task.Input {
		inputs = append(inputs, "- "+f.Path)
	}
	inputSection := "（无）"
	if len(inputs) > 0 {
		inputSection = strings.Join(inputs, "\n")
	}
	taskDir := filepath.Join(m.Store.Root, "tasks", task.ID)
	cliCmd := fmt.Sprintf("%s output <绝对文件路径...>", Quote(SelfBin()))
	return fmt.Sprintf(`# 任务：%s

%s

---
任务 ID：%s
Session ID：%s
工作目录：%s

先读取并遵循工作目录及其上级适用的 AGENTS.md / Agents.md / CLAUDE.md。
输入文件：
%s

项目产物遵循项目约定；无约定的产物存入：%s
创建产物后，用以下 CLI 回报每个实际存在的输出文件：
%s
回报环境变量已注入；勿把令牌写入回复。
由看板在任务完成时统一提交 Git 和归档。执行过程中保留其他人的改动，不自行 commit / push / 创建 PR。
最终回复说明实际结果、验证情况、关键决策和遗留问题。`,
		task.Title, desc, task.ID, session.ID, m.workingDir(task), inputSection, filepath.Join(taskDir, "output"), cliCmd)
}

func (m *Manager) Launch(task *Task, tool Tool, mode Mode, message string) (*Session, error) {
	m.mu.Lock()
	if m.launching[task.ID] {
		m.mu.Unlock()
		return nil, Fail("任务正在启动，请稍后。", http.StatusConflict)
	}
	m.launching[task.ID] = true
	defer func() {
		m.mu.Lock()
		delete(m.launching, task.ID)
		m.mu.Unlock()
	}()

	if tool == "" {
		tool = task.Tool
	}
	if mode == "" {
		mode = task.Mode
	}
	if err := ValidateMode(task.OneShot, mode); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	cwd, err := m.prepareLocked(task, tool, mode)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	now := NowISO()
	session := &Session{
		ID:           uuid.NewString(),
		Kind:         "work",
		Tool:         tool,
		Mode:         mode,
		Status:       SessionRunning,
		StartedAt:    now,
		StateSince:   now,
		ActiveMs:     0,
		Messages:     []Message{},
		RecentEvents: []string{},
	}
	task.Sessions = append(task.Sessions, session)
	task.Error = ""
	task.Git = nil
	task.Status = StatusDoing
	prompt := message
	if prompt == "" {
		prompt = m.buildPrompt(task, session)
	}
	err = m.spawnLocked(task, session, prompt, cwd)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) spawnLocked(task *Task, session *Session, prompt, cwd string) error {
	session.RunID = uuid.NewString()
	session.EventOffset = 0
	session.RecentEvents = []string{}
	session.ExitCode = nil
	session.Error = ""
	m.transition(session, SessionRunning, "")

	dir := m.RunDir(task, session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := hex.EncodeToString(tokenBytes)

	specPath := filepath.Join(dir, "launch.json")
	taskDir := filepath.Join(m.Store.Root, "tasks", task.ID)
	self := SelfBin()
	cmdSpec := CommandFor(LaunchOptions{
		Session:    session,
		Prompt:     prompt,
		Cwd:        cwd,
		TaskDir:    taskDir,
		RunDir:     dir,
		AppRoot:    m.Options.AppRoot,
		Executable: m.Options.Executables[session.Tool],
	})
	spec := LaunchSpec{
		Command:    cmdSpec.Command,
		Args:       cmdSpec.Args,
		Stdin:      cmdSpec.Stdin,
		Mode:       session.Mode,
		Cwd:        cwd,
		RunID:      session.RunID,
		LogPath:    m.LogPath(task, session),
		FinalPath:  filepath.Join(dir, "final.md"),
		ResultPath: filepath.Join(dir, "result.json"),
		Env: map[string]string{
			"AGENT_KANBAN_TASK_ID":    task.ID,
			"AGENT_KANBAN_SESSION_ID": session.ID,
			"AGENT_KANBAN_RUN_ID":     session.RunID,
			"AGENT_KANBAN_TOKEN":      token,
			"AGENT_KANBAN_URL":        m.Options.URL,
			"AGENT_KANBAN_EVENTS":     filepath.Join(dir, "events.jsonl"),
			"LOOM_BIN":                self,
		},
	}

	if session.Kind == "work" {
		_ = os.WriteFile(filepath.Join(taskDir, "prompt.md"), []byte(m.buildPrompt(task, session)), 0o600)
	}
	_ = os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(prompt), 0o600)
	specBytes, _ := json.Marshal(spec)
	_ = os.WriteFile(specPath, specBytes, 0o600)
	m.Store.Touch(task)

	var spawnErr error
	if session.Mode == ModeTmux {
		session.TmuxName = "ak-" + session.ID
		shellCmd := strings.Join([]string{Quote(self), "runner", Quote(specPath)}, " ")
		if err := exec.Command("tmux", "new-session", "-d", "-s", session.TmuxName, "-c", cwd, "-x", "140", "-y", "40", shellCmd).Run(); err != nil {
			spawnErr = err
		} else {
			_ = exec.Command("tmux", "pipe-pane", "-o", "-t", session.TmuxName, "cat >> "+Quote(m.LogPath(task, session))).Run()
		}
	} else {
		child := exec.Command(self, "runner", specPath)
		child.Dir = cwd
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			spawnErr = err
		} else {
			session.PID = child.Process.Pid
			go func() { _ = child.Wait() }()
		}
	}

	if spawnErr != nil {
		m.transition(session, SessionEnded, "")
		code := 1
		session.ExitCode = &code
		session.Error = spawnErr.Error()
		if session.Kind == "work" {
			task.Status = StatusWaiting
			task.Error = "启动失败：" + session.Error
		}
		m.Store.Touch(task)
		return spawnErr
	}

	m.Store.Touch(task)
	return nil
}

func (m *Manager) Chat(task *Task, session *Session, message string, targetMode Mode) (*Session, error) {
	m.mu.Lock()
	if session.Kind != "work" {
		m.mu.Unlock()
		return nil, Fail("归档 Agent 不支持继续对话。", http.StatusBadRequest)
	}
	if m.launching[task.ID] {
		m.mu.Unlock()
		return nil, Fail("任务正在启动，请稍后。", http.StatusConflict)
	}
	m.launching[task.ID] = true
	defer func() {
		m.mu.Lock()
		delete(m.launching, task.ID)
		m.mu.Unlock()
	}()

	if session.Status == SessionRunning {
		m.mu.Unlock()
		return nil, Fail("Agent 仍在运行，请等待本轮结束。", http.StatusConflict)
	}
	if session.NativeID == "" && session.Status == SessionEnded {
		m.mu.Unlock()
		return nil, Fail("未取得工具的原生 session ID，请使用「再开一个 Agent」。", http.StatusBadRequest)
	}
	task.OneShot = false
	if targetMode == "" {
		targetMode = session.Mode
	}
	cwd, err := m.prepareLocked(task, session.Tool, targetMode)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	task.Error = ""
	task.Git = nil
	session.Messages = append(session.Messages, Message{
		ID:   uuid.NewString(),
		Role: "user",
		Text: message,
		At:   NowISO(),
	})

	if session.Status == SessionAwaiting && session.Mode == ModeTmux {
		if len(message) > 64000 {
			m.mu.Unlock()
			return nil, Fail("消息过长，请缩短到 64000 个字符以内。", http.StatusBadRequest)
		}
		bufName := "ak-" + uuid.NewString()
		loadCmd := exec.Command("tmux", "load-buffer", "-b", bufName, "-")
		loadCmd.Stdin = strings.NewReader(message)
		if err := loadCmd.Run(); err != nil {
			m.mu.Unlock()
			return nil, errors.New("tmux 无法接收消息")
		}
		if err := exec.Command("tmux", "paste-buffer", "-d", "-b", bufName, "-t", session.TmuxName).Run(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		if err := exec.Command("tmux", "send-keys", "-t", session.TmuxName, "Enter").Run(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		m.transition(session, SessionRunning, "")
	} else {
		session.Mode = targetMode
		if err := m.spawnLocked(task, session, message, cwd); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}

	task.Status = StatusDoing
	m.Store.Touch(task)
	m.mu.Unlock()
	return session, nil
}

type AttachResult struct {
	Command string `json:"command"`
	Opened  bool   `json:"opened"`
}

func (m *Manager) Attach(task *Task, session *Session, openTerminal bool) (*AttachResult, error) {
	if session.Kind != "work" {
		return nil, Fail("归档 Agent 不支持 attach。", http.StatusBadRequest)
	}
	if task.Status == StatusDone {
		return nil, Fail("任务已完成。", http.StatusConflict)
	}
	if session.Mode == ModeHeadless || session.Status == SessionEnded {
		if _, err := m.Chat(task, session, "继续本任务，保留此前上下文。等待用户的下一条指令。", ModeTmux); err != nil {
			return nil, err
		}
	}
	command := "tmux attach-session -t " + Quote(session.TmuxName)
	opened := false
	if openTerminal {
		m.mu.Lock()
		script := filepath.Join(m.RunDir(task, session), "attach.command")
		m.mu.Unlock()
		_ = os.WriteFile(script, []byte("#!/bin/sh\nexec "+command+"\n"), 0o700)
		if err := exec.Command("open", "-a", "Terminal", script).Run(); err == nil {
			opened = true
		}
	}
	return &AttachResult{Command: command, Opened: opened}, nil
}

func (m *Manager) Stop(task *Task, session *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(task, session)
}

func (m *Manager) stopLocked(task *Task, session *Session) error {
	if session.Status == SessionEnded {
		return nil
	}
	if session.Mode == ModeTmux && session.TmuxName != "" {
		_ = exec.Command("tmux", "kill-session", "-t", session.TmuxName).Run()
	} else if session.PID > 0 {
		_ = syscall.Kill(session.PID, syscall.SIGTERM)
	}
	m.transition(session, SessionEnded, "")
	code := 130
	session.ExitCode = &code
	session.Error = "已由用户停止"
	if session.Kind == "work" {
		task.Error = "Session 已停止，可继续交流或再开一个 Agent。"
	}
	if session.Kind == "archive" {
		task.Archive = ArchiveState{Status: "error", Error: "归档已停止，可重试"}
	} else {
		agg := Aggregate(task.Status, task.OneShot, task.Sessions)
		if agg == StatusDone {
			task.Status = StatusWaiting
		} else {
			task.Status = agg
		}
	}
	m.Store.Touch(task)
	return nil
}

func (m *Manager) Report(task *Task, paths []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reportLocked(task, paths)
}

func (m *Manager) reportLocked(task *Task, paths []string) error {
	wdir, err := filepath.EvalSymlinks(m.workingDir(task))
	if err != nil {
		return err
	}
	tdir, err := filepath.EvalSymlinks(filepath.Join(m.Store.Root, "tasks", task.ID))
	if err != nil {
		return err
	}
	allowed := []string{wdir, tdir}

	for _, p := range paths {
		candidate := p
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(m.workingDir(task), candidate)
		}
		abs, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return Fail("输出文件不存在："+p, http.StatusBadRequest)
		}
		ok := false
		for _, root := range allowed {
			if inside(root, abs) {
				ok = true
				break
			}
		}
		if !ok {
			return Fail("输出文件必须位于项目或任务目录内："+p, http.StatusBadRequest)
		}
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() {
			return Fail("输出路径必须是文件："+p, http.StatusBadRequest)
		}
		exists := false
		for _, f := range task.Output {
			if f.Path == abs {
				exists = true
				break
			}
		}
		if !exists {
			task.Output = append(task.Output, TaskFile{
				ID:   uuid.NewString(),
				Path: abs,
				Name: filepath.Base(abs),
				Size: st.Size(),
			})
		}
	}
	m.Store.Touch(task)
	return nil
}

func (m *Manager) File(task *Task, fileID string) (*TaskFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fileLocked(task, fileID)
}

func (m *Manager) fileLocked(task *Task, fileID string) (*TaskFile, error) {
	var found *TaskFile
	for i := range task.Input {
		if task.Input[i].ID == fileID {
			f := task.Input[i]
			found = &f
			break
		}
	}
	if found == nil {
		for i := range task.Output {
			if task.Output[i].ID == fileID {
				f := task.Output[i]
				found = &f
				break
			}
		}
	}
	if found == nil {
		return nil, Fail("文件不存在", http.StatusNotFound)
	}
	abs, err := filepath.EvalSymlinks(found.Path)
	if err != nil {
		return nil, Fail("文件已移动或不在任务目录内。", http.StatusForbidden)
	}
	wdir, _ := filepath.EvalSymlinks(m.workingDir(task))
	tdir, _ := filepath.EvalSymlinks(filepath.Join(m.Store.Root, "tasks", task.ID))
	allowed := (wdir != "" && inside(wdir, abs)) || (tdir != "" && inside(tdir, abs))
	st, err := os.Stat(abs)
	if !allowed || err != nil || !st.Mode().IsRegular() {
		return nil, Fail("文件已移动或不在任务目录内。", http.StatusForbidden)
	}
	out := *found
	out.Path = abs
	return &out, nil
}

func (m *Manager) Authenticate(ev Event, token string) error {
	m.mu.Lock()
	task, err := m.Store.Task(ev.TaskID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	var session *Session
	for _, s := range task.Sessions {
		if s.ID == ev.SessionID {
			session = s
			break
		}
	}
	if session == nil || session.RunID != ev.RunID {
		m.mu.Unlock()
		return Fail("Session 已失效", http.StatusConflict)
	}
	runDir := m.RunDir(task, session)
	m.mu.Unlock()

	specBytes, err := os.ReadFile(filepath.Join(runDir, "launch.json"))
	if err != nil {
		return Fail("Session 已失效", http.StatusConflict)
	}
	var spec LaunchSpec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return Fail("Session 已失效", http.StatusConflict)
	}
	expected := []byte(spec.Env["AGENT_KANBAN_TOKEN"])
	supplied := []byte(token)
	if len(expected) == 0 || len(expected) != len(supplied) || subtle.ConstantTimeCompare(expected, supplied) != 1 {
		return Fail("Hook 令牌无效", http.StatusUnauthorized)
	}

	line, _ := json.Marshal(ev)
	if f, err := os.OpenFile(filepath.Join(runDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}
	go func() { _ = m.Tick() }()
	return nil
}

func (m *Manager) applyEventLocked(task *Task, session *Session, ev Event) {
	if ev.RunID != session.RunID || slices.Contains(session.RecentEvents, ev.ID) {
		return
	}
	session.RecentEvents = append(session.RecentEvents, ev.ID)
	if len(session.RecentEvents) > 512 {
		session.RecentEvents = session.RecentEvents[len(session.RecentEvents)-512:]
	}
	if ev.NativeID != "" {
		session.NativeID = ev.NativeID
	}
	if ev.Text != "" && (ev.Type == "awaiting" || ev.Type == "message") {
		n := len(session.Messages)
		if n == 0 || session.Messages[n-1].Role != "assistant" || session.Messages[n-1].Text != ev.Text {
			session.Messages = append(session.Messages, Message{
				ID:   ev.ID,
				Role: "assistant",
				Text: ev.Text,
				At:   ev.At,
			})
		}
	}
	if ev.Type == "output" {
		if err := m.reportLocked(task, ev.Paths); err != nil {
			task.Error = "输出回报失败：" + err.Error()
		}
	}
	if session.Status == SessionEnded {
		return
	}

	switch ev.Type {
	case "exit":
		m.transition(session, SessionEnded, ev.At)
		code := 1
		if ev.ExitCode != nil {
			code = *ev.ExitCode
		}
		session.ExitCode = &code
		if ev.Error != "" {
			session.Error = ev.Error
		} else if code != 0 {
			session.Error = fmt.Sprintf("进程以状态 %d 退出，请查看日志", code)
		}
	case "running":
		m.transition(session, SessionRunning, ev.At)
	case "awaiting":
		if session.Mode == ModeTmux {
			m.transition(session, SessionAwaiting, ev.At)
			if ev.Error != "" {
				session.Error = ev.Error
			}
		}
	}
}

func (m *Manager) GitReview(task *Task) (*GitReview, error) {
	res, err := Review(task)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if task.Baseline != nil && len(res.Files) > 0 {
		for _, other := range m.Store.Data.Tasks {
			if other.ID != task.ID && other.Status != StatusDone && other.Baseline != nil && other.Baseline.Root == task.Baseline.Root {
				for _, s := range other.Sessions {
					if s.Kind == "work" {
						res.Blocked = fmt.Sprintf("同一仓库还有未完成任务「%s」，请先在外部核对改动归属并提交，或为任务使用独立工作目录。", other.Title)
						break
					}
				}
			}
			if res.Blocked != "" {
				break
			}
		}
	}
	return res, nil
}

func (m *Manager) Finish(task *Task, automatic bool, action, message, token string) error {
	m.mu.Lock()
	if m.completing[task.ID] {
		m.mu.Unlock()
		return Fail("完成流程正在运行。", http.StatusConflict)
	}
	if task.Status == StatusDone {
		m.mu.Unlock()
		return Fail("任务已经完成。", http.StatusConflict)
	}
	var work []*Session
	for _, s := range task.Sessions {
		if s.Kind == "work" {
			work = append(work, s)
		}
	}
	if len(work) == 0 {
		m.mu.Unlock()
		return Fail("请等待所有 Agent 停止后确认。", http.StatusConflict)
	}
	for _, s := range work {
		if s.Status == SessionRunning {
			m.mu.Unlock()
			return Fail("请等待所有 Agent 停止后确认。", http.StatusConflict)
		}
		if automatic && (s.Status != SessionEnded || s.ExitCode == nil || *s.ExitCode != 0 || s.Error != "") {
			m.mu.Unlock()
			return Fail("执行失败的任务需要人工处理。", http.StatusConflict)
		}
	}
	m.mu.Unlock()

	info, err := m.GitReview(task)
	if err != nil {
		return err
	}
	if info.Blocked != "" {
		return Fail(info.Blocked, http.StatusConflict)
	}
	if token != "" && token != info.Token {
		return Fail("diff 已更新，请重新查看后确认。", http.StatusConflict)
	}
	if !automatic && token == "" {
		return Fail("请先查看 diff 并确认。", http.StatusConflict)
	}

	m.mu.Lock()
	var root string
	if task.Baseline != nil {
		root = task.Baseline.Root
	}
	if root != "" && m.repoLocks[root] {
		m.mu.Unlock()
		return Fail("项目正在提交，请稍后重试。", http.StatusConflict)
	}
	m.completing[task.ID] = true
	if root != "" {
		m.repoLocks[root] = true
	}
	task.Completion = &CompletionState{Automatic: automatic, Action: action, Message: message}
	task.Error = ""
	m.Store.Touch(task)

	for _, s := range work {
		if s.Status == SessionAwaiting {
			_ = m.stopLocked(task, s)
			s.Error = ""
			zero := 0
			s.ExitCode = &zero
		}
	}
	taskDir := filepath.Join(m.Store.Root, "tasks", task.ID)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		task.Completion = nil
		delete(m.completing, task.ID)
		if root != "" {
			delete(m.repoLocks, root)
		}
		m.Store.Touch(task)
		m.mu.Unlock()
	}()

	expectTok := token
	if expectTok == "" {
		expectTok = info.Token
	}

	needCommit := task.Git == nil || task.Git.Status != "committed" || len(info.Files) > 0 || info.Head != task.Git.Hash
	if needCommit {
		gitRes, err := CommitTask(task, expectTok, filepath.Join(taskDir, "commit-message.txt"))
		if err != nil {
			m.mu.Lock()
			if task.Status != StatusDone {
				task.Status = StatusWaiting
				task.Error = err.Error()
				if task.Git == nil || task.Git.Status != "committed" {
					task.Git = &GitResult{Status: "error", Automatic: automatic, Error: task.Error}
				}
			}
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		task.Git = gitRes
		m.Store.Touch(task)
		m.mu.Unlock()
	}

	if action == "pr" {
		if task.Baseline == nil {
			err := Fail("工作目录未使用 Git，无法创建 PR。", http.StatusBadRequest)
			m.mu.Lock()
			task.Status = StatusWaiting
			task.Error = err.Error()
			m.mu.Unlock()
			return err
		}
		prURL, err := CreatePR(task, filepath.Join(taskDir, "pr-body.md"))
		if err != nil {
			m.mu.Lock()
			task.Status = StatusWaiting
			task.Error = err.Error()
			m.mu.Unlock()
			return err
		}
		m.mu.Lock()
		task.Git.PRURL = prURL
		m.mu.Unlock()
	}

	m.mu.Lock()
	task.Status = StatusDone
	task.Error = ""
	m.Store.Touch(task)
	m.mu.Unlock()

	return m.Archive(task)
}

var textExtRe = regexp.MustCompile(`(?i)\.(md|txt)$`)
var fenceStartRe = regexp.MustCompile(`^` + "```" + `(?:markdown|md)?\s*\n`)
var fenceEndRe = regexp.MustCompile(`\n` + "```" + `$`)
var invalidFileCharsRe = regexp.MustCompile(`[\x00-\x1f/\\:*?"<>|]`)

func (m *Manager) Archive(task *Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.archiveLocked(task)
}

func (m *Manager) archiveLocked(task *Task) error {
	if task.Status != StatusDone {
		return Fail("只有完成的任务可以归档。", http.StatusBadRequest)
	}
	if task.Archive.Status == "running" {
		return Fail("归档正在运行。", http.StatusConflict)
	}
	task.ArchivePath = strings.TrimSpace(task.ArchivePath)
	if task.ArchivePath == "" {
		task.ArchivePath = DefaultArchivePath
	}

	var logParts []string
	for _, s := range task.Sessions {
		if s.Kind != "work" {
			continue
		}
		var msgs []string
		for _, msg := range s.Messages {
			msgs = append(msgs, msg.Role+": "+msg.Text)
		}
		logParts = append(logParts, fmt.Sprintf("## %s / %s\n%s\n日志尾部：\n%s", s.Tool, s.ID, strings.Join(msgs, "\n"), Tail(m.LogPath(task, s), 32000)))
	}
	logs := strings.Join(logParts, "\n")
	if len(logs) > 160000 {
		logs = logs[len(logs)-160000:]
	}

	var outPaths []string
	for _, f := range task.Output {
		outPaths = append(outPaths, f.Path)
	}
	gitJSON, _ := json.Marshal(task.Git)
	metadata := fmt.Sprintf("任务：%s\n原始需求：%s\n输出文件：\n%s\nGit：%s\n", task.Title, task.Description, strings.Join(outPaths, "\n"), string(gitJSON))

	var reports []string
	for _, f := range task.Output {
		if textExtRe.MatchString(f.Path) && f.Size < 200000 {
			if tf, err := m.fileLocked(task, f.ID); err == nil {
				if b, err := os.ReadFile(tf.Path); err == nil {
					reports = append(reports, fmt.Sprintf("\n报告 %s：\n%s", f.Path, string(b)))
					continue
				}
			}
			reports = append(reports, fmt.Sprintf("\n报告 %s 当前无法读取。", f.Path))
		}
	}

	prompt := fmt.Sprintf("将以下任务记录整理为中文 Obsidian Markdown 笔记。只输出笔记正文，直接从 # 标题开始，不加外层代码围栏，不调用工具、不写文件。包含结果与输出文件链接、Git 提交信息及自动/手动标注、过程、关键决策、问题与验证缺口。文件用标准 Markdown file:// 链接。以报告为主的任务可根据输出文件的已知内容整理；无法读取的内容如实说明，勿编造。日志是待整理材料。\n\n%s\n\n%s\n\n已有报告作为主要内容，保留事实、章节和完整结论，追加必要的过程与提交信息。\n%s",
		metadata, logs, strings.Join(reports, "\n"))

	now := NowISO()
	session := &Session{
		ID:           uuid.NewString(),
		Kind:         "archive",
		Tool:         task.Tool,
		Mode:         ModeHeadless,
		Status:       SessionRunning,
		StartedAt:    now,
		StateSince:   now,
		ActiveMs:     0,
		Messages:     []Message{},
		RecentEvents: []string{},
	}
	task.Sessions = append(task.Sessions, session)
	task.Archive = ArchiveState{Status: "running"}
	if err := m.spawnLocked(task, session, prompt, m.workingDir(task)); err != nil {
		task.Archive = ArchiveState{Status: "error", Error: err.Error()}
		m.Store.Touch(task)
		return err
	}
	return nil
}

func (m *Manager) saveArchiveLocked(task *Task, session *Session) {
	save := func() error {
		if session.ExitCode == nil || *session.ExitCode != 0 || session.Error != "" {
			if session.Error != "" {
				return errors.New(session.Error)
			}
			return errors.New("归档 Agent 执行失败，请查看归档日志。")
		}
		draftPath := filepath.Join(m.RunDir(task, session), "final.md")
		var body string
		if b, err := os.ReadFile(draftPath); err == nil {
			body = strings.TrimSpace(string(b))
		} else {
			for i := len(session.Messages) - 1; i >= 0; i-- {
				if session.Messages[i].Role == "assistant" {
					body = strings.TrimSpace(session.Messages[i].Text)
					break
				}
			}
		}
		if body == "" {
			return errors.New("归档 Agent 未返回笔记内容。")
		}
		body = fenceStartRe.ReplaceAllString(body, "")
		body = fenceEndRe.ReplaceAllString(body, "")

		titleJSON, _ := json.Marshal(task.Title)
		commitHash := ""
		commitMode := "manual"
		if task.Git != nil {
			commitHash = task.Git.Hash
			if task.Git.Automatic {
				commitMode = "automatic"
			}
		}
		commitJSON, _ := json.Marshal(commitHash)
		metadata := fmt.Sprintf("---\ntitle: %s\ntask_id: %s\ndate: %s\nstatus: done\ncommit_mode: %s\ncommit: %s\n---\n\n",
			string(titleJSON), task.ID, NowISO()[:10], commitMode, string(commitJSON))

		mainBody := body
		if len(task.Output) == 1 && textExtRe.MatchString(task.Output[0].Path) && task.Output[0].Size < 200000 {
			if tf, err := m.fileLocked(task, task.Output[0].ID); err == nil {
				if b, err := os.ReadFile(tf.Path); err == nil {
					mainBody = string(b) + "\n\n## 任务过程记录\n\n" + body
				}
			}
		}

		var outLinks []string
		for _, f := range task.Output {
			escaped := strings.ReplaceAll(strings.ReplaceAll(f.Name, "[", `\[`), "]", `\]`)
			u := url.URL{Scheme: "file", Path: f.Path}
			outLinks = append(outLinks, fmt.Sprintf("- [%s](%s)", escaped, u.String()))
		}
		outputSection := "未回报输出文件。"
		if len(outLinks) > 0 {
			outputSection = strings.Join(outLinks, "\n")
		}

		gitRecord := "无 Git 提交"
		if task.Git != nil {
			if task.Git.Status == "committed" {
				modeLabel := "手动"
				if task.Git.Automatic {
					modeLabel = "自动"
				}
				commitRef := task.Git.Hash
				if task.Git.URL != "" {
					commitRef = fmt.Sprintf("[%s](%s)", task.Git.Hash, task.Git.URL)
				}
				prLine := ""
				if task.Git.PRURL != "" {
					prLine = "\n\nPR：[查看 PR](" + task.Git.PRURL + ")"
				}
				gitRecord = fmt.Sprintf("提交方式：%s\n\nCommit：%s\n\n提交信息：\n\n```text\n%s\n```%s", modeLabel, commitRef, task.Git.Message, prLine)
			} else if task.Git.Message != "" {
				gitRecord = task.Git.Message
			}
		}

		content := fmt.Sprintf("%s%s\n\n---\n\n## 任务与产物\n\n任务 ID：%s\n\n%s\n\n%s\n", metadata, mainBody, task.ID, gitRecord, outputSection)
		_ = os.WriteFile(filepath.Join(m.Store.Root, "archive", task.ID+".md"), []byte(content), 0o600)

		dir := Expand(task.ArchivePath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		name := invalidFileCharsRe.ReplaceAllString(task.Title, "-")
		runes := []rune(name)
		if len(runes) > 80 {
			name = string(runes[:80])
		}
		name = strings.TrimRight(name, ". ")
		if name == "" {
			name = "任务"
		}
		shortID := task.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		notePath := filepath.Join(dir, fmt.Sprintf("%s · %s.md", name, shortID))
		if b, err := os.ReadFile(notePath); err == nil {
			if !strings.Contains(string(b), "task_id: "+task.ID) {
				return errors.New("归档目标已存在，未覆盖：" + notePath)
			}
		} else {
			f, err := os.OpenFile(notePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, _ = f.Write([]byte(content))
			_ = f.Close()
		}
		task.Archive = ArchiveState{Status: "done", Path: notePath}
		return nil
	}

	if err := save(); err != nil {
		task.Archive = ArchiveState{Status: "error", Error: err.Error()}
	}
	m.Store.Touch(task)
}

func (m *Manager) Tick() error {
	m.mu.Lock()
	if m.ticking {
		m.mu.Unlock()
		return nil
	}
	m.ticking = true
	tasks := slices.Clone(m.Store.Data.Tasks)
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.ticking = false
		m.mu.Unlock()
	}()

	for _, task := range tasks {
		m.mu.Lock()
		changed := false
		for _, session := range task.Sessions {
			dir := m.RunDir(task, session)
			journal := filepath.Join(dir, "events.jsonl")
			if st, err := os.Stat(journal); err == nil && st.Size() > session.EventOffset {
				if f, err := os.Open(journal); err == nil {
					readLen := min(st.Size()-session.EventOffset, 4*1024*1024)
					buf := make([]byte, readLen)
					n, _ := f.ReadAt(buf, session.EventOffset)
					_ = f.Close()
					buf = buf[:n]
					if lastNL := strings.LastIndexByte(string(buf), '\n'); lastNL >= 0 {
						lines := strings.Split(string(buf[:lastNL]), "\n")
						for _, line := range lines {
							if line == "" {
								continue
							}
							var ev Event
							if err := json.Unmarshal([]byte(line), &ev); err != nil {
								task.Error = "状态回报异常：" + err.Error()
							} else {
								m.applyEventLocked(task, session, ev)
							}
						}
						session.EventOffset += int64(lastNL + 1)
						changed = true
					}
				}
			}

			resultPath := filepath.Join(dir, "result.json")
			if session.Status != SessionEnded {
				if b, err := os.ReadFile(resultPath); err == nil {
					var res Event
					if json.Unmarshal(b, &res) == nil {
						res.ID = "process-exit-" + session.RunID
						res.Type = "exit"
						res.At = NowISO()
						m.applyEventLocked(task, session, res)
						changed = true
					}
				} else if !m.launching[task.ID] && !m.completing[task.ID] {
					alive := false
					if session.Mode == ModeTmux && session.TmuxName != "" {
						m.mu.Unlock()
						err := exec.Command("tmux", "has-session", "-t", session.TmuxName).Run()
						m.mu.Lock()
						alive = err == nil
					} else if session.PID > 0 {
						err := syscall.Kill(session.PID, 0)
						alive = err == nil || errors.Is(err, syscall.EPERM)
					}
					if !alive && time.Now().UnixMilli()-parseTimeMs(session.StateSince) > 4000 {
						one := 1
						m.applyEventLocked(task, session, Event{
							ID:        "lost-" + session.RunID,
							Type:      "exit",
							RunID:     session.RunID,
							TaskID:    task.ID,
							SessionID: session.ID,
							At:        NowISO(),
							ExitCode:  &one,
							Error:     "未找到运行进程，请查看日志后重试。",
						})
						changed = true
					}
				}
			}

			var curArchive *Session
			for i := len(task.Sessions) - 1; i >= 0; i-- {
				if task.Sessions[i].Kind == "archive" {
					curArchive = task.Sessions[i]
					break
				}
			}
			if curArchive != nil && session.ID == curArchive.ID && session.Status == SessionEnded && task.Archive.Status == "running" {
				m.saveArchiveLocked(task, session)
			}
		}

		needAutoFinish := false
		if task.Status != StatusDone && !m.launching[task.ID] && !m.completing[task.ID] {
			if task.Completion != nil {
				task.Completion = nil
				task.Error = "服务在完成流程中重启，请重新查看 diff 并确认；已有提交会被保留。"
				changed = true
			}
			status := Aggregate(task.Status, task.OneShot, task.Sessions)
			if status == StatusDone && task.Error == "" {
				needAutoFinish = true
			} else {
				next := status
				if next == StatusDone {
					next = StatusWaiting
				}
				if next != task.Status {
					task.Status = next
					changed = true
				}
			}
		}
		if changed {
			m.Store.Touch(task)
		}
		needAutoArchive := task.Status == StatusDone && task.Archive.Status == "idle" && !m.completing[task.ID] && !needAutoFinish
		m.mu.Unlock()

		if needAutoFinish {
			msg := fmt.Sprintf("feat: task %s\n\n- 完成任务 %s 的改动。", task.Title, task.ID)
			if err := m.Finish(task, true, "commit", msg, ""); err != nil {
				m.mu.Lock()
				task.Status = StatusWaiting
				task.Error = err.Error()
				m.Store.Touch(task)
				m.mu.Unlock()
			}
		} else if needAutoArchive {
			_ = m.Archive(task)
		}
	}
	return nil
}
