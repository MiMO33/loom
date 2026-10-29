package kanban

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ValidThemes    = []string{"system", "light", "dark"}
	ValidTextSizes = []int{100, 110, 125, 150}
	ValidDocks     = []string{"always", "window", "never"}
	validBundleID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)
)

type Settings struct {
	Theme              string          `json:"theme,omitempty"`
	TextSize           int             `json:"textSize,omitempty"`
	Dock               string          `json:"dock,omitempty"`
	KeepAwake          bool            `json:"keepAwake,omitempty"`
	KeepAwakeDisplay   bool            `json:"keepAwakeDisplay,omitempty"`
	NotifyTaskEnd      *bool           `json:"notifyTaskEnd,omitempty"`
	SessionTerminal    string          `json:"sessionTerminal,omitempty"`
	DefaultTool        Tool            `json:"defaultTool,omitempty"`
	DefaultMode        Mode            `json:"defaultMode,omitempty"`
	DefaultOneShot     *bool           `json:"defaultOneShot,omitempty"`
	DefaultArchivePath string          `json:"defaultArchivePath,omitempty"`
	Executables        map[Tool]string `json:"executables,omitempty"`
}

func (s Settings) Normalized() Settings {
	if !slices.Contains(ValidThemes, s.Theme) {
		s.Theme = "system"
	}
	if !slices.Contains(ValidTextSizes, s.TextSize) {
		s.TextSize = 100
	}
	if !slices.Contains(ValidDocks, s.Dock) {
		s.Dock = "always"
	}
	if s.NotifyTaskEnd == nil {
		b := true
		s.NotifyTaskEnd = &b
	}
	if s.SessionTerminal == "system" {
		s.SessionTerminal = ""
	}
	if !validateTool(s.DefaultTool) {
		s.DefaultTool = ToolClaude
	}
	if !validateModeValue(s.DefaultMode) {
		s.DefaultMode = ModeHeadless
	}
	if s.DefaultOneShot == nil {
		b := true
		s.DefaultOneShot = &b
	}
	if *s.DefaultOneShot {
		s.DefaultMode = ModeHeadless
	}
	s.DefaultArchivePath = strings.TrimSpace(s.DefaultArchivePath)
	if s.DefaultArchivePath == "" {
		s.DefaultArchivePath = DefaultArchivePath
	}
	cleanExec := make(map[Tool]string)
	for _, t := range ValidTools {
		if v := strings.TrimSpace(s.Executables[t]); v != "" {
			cleanExec[t] = v
		}
	}
	if len(cleanExec) > 0 {
		s.Executables = cleanExec
	} else {
		s.Executables = nil
	}
	return s
}

func ValidateSettings(s Settings) (Settings, error) {
	if s.Theme != "" && !slices.Contains(ValidThemes, s.Theme) {
		return Settings{}, Fail(fmt.Sprintf("外观主题必须是 %v 之一", ValidThemes), 400)
	}
	if s.TextSize != 0 && !slices.Contains(ValidTextSizes, s.TextSize) {
		return Settings{}, Fail(fmt.Sprintf("文字大小必须是 %v 之一", ValidTextSizes), 400)
	}
	if s.Dock != "" && !slices.Contains(ValidDocks, s.Dock) {
		return Settings{}, Fail(fmt.Sprintf("Dock 显示选项无效：%s", s.Dock), 400)
	}
	if s.SessionTerminal != "" && s.SessionTerminal != "system" && !validBundleID.MatchString(s.SessionTerminal) {
		return Settings{}, Fail("终端应用标识格式无效", 400)
	}
	if s.DefaultTool != "" && !validateTool(s.DefaultTool) {
		return Settings{}, Fail("默认 Agent 工具无效", 400)
	}
	if s.DefaultMode != "" && !validateModeValue(s.DefaultMode) {
		return Settings{}, Fail("默认运行模式无效", 400)
	}
	if s.DefaultOneShot != nil && *s.DefaultOneShot && s.DefaultMode == ModeTmux {
		return Settings{}, Fail("一次性任务默认使用 headless 模式", 400)
	}
	return s.Normalized(), nil
}

type TerminalChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

type SettingsView struct {
	Settings
	Login           bool             `json:"login"`
	NotifyProblem   string           `json:"notifyProblem,omitempty"`
	Version         string           `json:"version"`
	DataDir         string           `json:"dataDir"`
	URL             string           `json:"url"`
	TerminalApps    []TerminalChoice `json:"terminalApps"`
	TerminalDefault string           `json:"terminalDefault"`
}

func ShortFolderName(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `/\`)
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

const launchAgentLabel = "com.yetone.loom"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func IsLoginEnabled() bool {
	b, err := os.ReadFile(launchAgentPath())
	return err == nil && strings.Contains(string(b), "<string>"+launchAgentLabel+"</string>")
}

func SetLoginEnabled(on bool) error {
	p := launchAgentPath()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe := SelfBin()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>tray</string></array>
	<key>RunAtLoad</key><true/>
	<key>LimitLoadToSessionType</key><string>Aqua</string>
	<key>ProcessType</key><string>Interactive</string>
	<key>AbandonProcessGroup</key><true/>
</dict>
</plist>
`, launchAgentLabel, exe)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(plist), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

var terminalAppsOnce sync.Once
var cachedTerminals []TerminalChoice
var cachedDefaultTerminal string

func DiscoverTerminalApps() ([]TerminalChoice, string) {
	terminalAppsOnce.Do(func() {
		candidates := []struct {
			ID    string
			Name  string
			Paths []string
		}{
			{ID: "com.apple.Terminal", Name: "Terminal", Paths: []string{"/System/Applications/Utilities/Terminal.app", "/Applications/Utilities/Terminal.app"}},
			{ID: "com.googlecode.iterm2", Name: "iTerm", Paths: []string{"/Applications/iTerm.app", filepath.Join(os.Getenv("HOME"), "Applications/iTerm.app")}},
			{ID: "com.mitchellh.ghostty", Name: "Ghostty", Paths: []string{"/Applications/Ghostty.app", filepath.Join(os.Getenv("HOME"), "Applications/Ghostty.app")}},
			{ID: "dev.warp.Warp-Stable", Name: "Warp", Paths: []string{"/Applications/Warp.app", filepath.Join(os.Getenv("HOME"), "Applications/Warp.app")}},
			{ID: "net.kovidgoyal.kitty", Name: "kitty", Paths: []string{"/Applications/kitty.app", filepath.Join(os.Getenv("HOME"), "Applications/kitty.app")}},
			{ID: "org.alacritty", Name: "Alacritty", Paths: []string{"/Applications/Alacritty.app", filepath.Join(os.Getenv("HOME"), "Applications/Alacritty.app")}},
			{ID: "com.github.wez.wezterm", Name: "WezTerm", Paths: []string{"/Applications/WezTerm.app", filepath.Join(os.Getenv("HOME"), "Applications/WezTerm.app")}},
			{ID: "co.zeit.hyper", Name: "Hyper", Paths: []string{"/Applications/Hyper.app", filepath.Join(os.Getenv("HOME"), "Applications/Hyper.app")}},
		}
		for _, c := range candidates {
			for _, p := range c.Paths {
				if st, err := os.Stat(p); err == nil && st.IsDir() {
					cachedTerminals = append(cachedTerminals, TerminalChoice{
						ID:   c.ID,
						Name: c.Name,
						Path: p,
					})
					break
				}
			}
		}
		if len(cachedTerminals) == 0 {
			cachedTerminals = []TerminalChoice{{ID: "com.apple.Terminal", Name: "Terminal", Path: "/System/Applications/Utilities/Terminal.app"}}
		}
		cachedDefaultTerminal = "com.apple.Terminal"
	})
	return cachedTerminals, cachedDefaultTerminal
}

func TerminalOpenArgs(choice string) []string {
	apps, _ := DiscoverTerminalApps()
	if choice == "" || choice == "system" || choice == "com.apple.Terminal" {
		return []string{"-a", "Terminal"}
	}
	for _, app := range apps {
		if app.ID == choice && app.Path != "" {
			return []string{"-a", app.Path}
		}
	}
	return []string{"-b", choice}
}

type CLIShell struct {
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
	Known   bool   `json:"known"`
	Command string `json:"command,omitempty"`
	Ours    bool   `json:"ours"`
	Profile string `json:"profile,omitempty"`
	HasDir  bool   `json:"hasDir,omitempty"`
}

type CLIStatus struct {
	Exe     string     `json:"exe"`
	Command string     `json:"command,omitempty"`
	Ours    bool       `json:"ours"`
	Dir     string     `json:"dir,omitempty"`
	Shells  []CLIShell `json:"shells"`
}

func tildePath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func sameExecutable(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil && ra == rb {
		return true
	}
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

func ReadCLIStatus() *CLIStatus {
	exe := SelfBin()
	status := &CLIStatus{Exe: exe, Shells: []CLIShell{}}
	home, _ := os.UserHomeDir()
	defaultShell := filepath.Base(os.Getenv("SHELL"))
	if defaultShell == "" || defaultShell == "." {
		defaultShell = "zsh"
	}

	shellDefs := []struct {
		name    string
		bin     string
		profile string
	}{
		{"zsh", "/bin/zsh", filepath.Join(home, ".zshrc")},
		{"bash", "/bin/bash", filepath.Join(home, ".zshrc")},
		{"fish", "/opt/homebrew/bin/fish", filepath.Join(home, ".config/fish/config.fish")},
	}
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
		shellDefs[1].profile = filepath.Join(home, ".bashrc")
	} else {
		shellDefs[1].profile = filepath.Join(home, ".bash_profile")
	}
	if _, err := os.Stat(shellDefs[2].bin); err != nil {
		if p, err := exec.LookPath("fish"); err == nil {
			shellDefs[2].bin = p
		}
	}

	localBin := filepath.Join(home, ".local", "bin")
	status.Dir = tildePath(localBin)

	for _, sd := range shellDefs {
		if _, err := os.Stat(sd.bin); err != nil {
			continue
		}
		isDef := sd.name == defaultShell
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, sd.bin, "-lc", "command -v loom 2>/dev/null || true; printf '\\n__PATH__\\n%s' \"$PATH\"").Output()
		cancel()
		known := err == nil
		var foundCmd string
		var pathDirs []string
		if known {
			parts := strings.SplitN(string(out), "\n__PATH__\n", 2)
			foundCmd = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				pathDirs = filepath.SplitList(strings.TrimSpace(parts[1]))
			}
		}
		ours := foundCmd != "" && sameExecutable(foundCmd, exe)
		hasDir := slices.Contains(pathDirs, localBin)
		sh := CLIShell{
			Name:    sd.name,
			Default: isDef,
			Known:   known,
			Command: foundCmd,
			Ours:    ours,
			Profile: tildePath(sd.profile),
			HasDir:  hasDir,
		}
		if isDef {
			status.Command = foundCmd
			status.Ours = ours
		}
		status.Shells = append(status.Shells, sh)
	}
	if status.Command == "" && len(status.Shells) > 0 {
		status.Command = status.Shells[0].Command
		status.Ours = status.Shells[0].Ours
	}
	return status
}

func InstallCLI(shellName string) (*CLIStatus, error) {
	exe := SelfBin()
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, Fail("无法读取用户主目录", 500)
	}
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o755); err != nil {
		return nil, err
	}
	linkPath := filepath.Join(localBin, "loom")
	_ = os.Remove(linkPath)
	if err := os.Symlink(exe, linkPath); err != nil {
		return nil, err
	}

	if shellName != "" {
		var profile string
		var line string
		switch shellName {
		case "zsh":
			profile = filepath.Join(home, ".zshrc")
			line = `export PATH="$HOME/.local/bin:$PATH"`
		case "bash":
			profile = filepath.Join(home, ".bash_profile")
			if _, err := os.Stat(filepath.Join(home, ".bashrc")); err == nil {
				profile = filepath.Join(home, ".bashrc")
			}
			line = `export PATH="$HOME/.local/bin:$PATH"`
		case "fish":
			profile = filepath.Join(home, ".config/fish/config.fish")
			line = `fish_add_path -g ~/.local/bin`
		default:
			return nil, Fail("不支持的 Shell："+shellName, 400)
		}
		if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
			return nil, err
		}
		existing, _ := os.ReadFile(profile)
		if !strings.Contains(string(existing), ".local/bin") {
			content := string(existing)
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += "\n# Added by Loom\n" + line + "\n"
			if err := os.WriteFile(profile, []byte(content), 0o644); err != nil {
				return nil, err
			}
		}
	}
	return ReadCLIStatus(), nil
}

func (m *Manager) syncAwakeLocked() {
	s := m.Store.Data.Settings.Normalized()
	hasRunning := false
	for _, t := range m.Store.Data.Tasks {
		for _, sess := range t.Sessions {
			if sess.Status == SessionRunning {
				hasRunning = true
				break
			}
		}
		if hasRunning {
			break
		}
	}
	wantMode := 0 // 0=off, 1=system, 2=display
	if s.KeepAwake && hasRunning {
		wantMode = 1
		if s.KeepAwakeDisplay {
			wantMode = 2
		}
	}
	if m.awakeMode == wantMode {
		return
	}
	if m.awakeCancel != nil {
		m.awakeCancel()
		m.awakeCancel = nil
		m.awakeMode = 0
	}
	if wantMode > 0 {
		args := []string{"-i"}
		if wantMode == 2 {
			args = append(args, "-d")
		}
		args = append(args, "-w", strconv.Itoa(os.Getpid()))
		cmd := exec.Command("/usr/bin/caffeinate", args...)
		if err := cmd.Start(); err == nil {
			m.awakeMode = wantMode
			m.awakeCancel = func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	}
}

func (m *Manager) ExecutableFor(tool Tool) string {
	if custom := strings.TrimSpace(m.Options.Executables[tool]); custom != "" {
		return Expand(custom)
	}
	if custom := strings.TrimSpace(m.Store.Data.Settings.Executables[tool]); custom != "" {
		return Expand(custom)
	}
	return DefaultCommandForTool(tool)
}
