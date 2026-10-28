//go:build darwin

package proc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AgentVars are environment variables that coding agents read from the user's
// shell profile and that a Finder/Dock-launched macOS app wouldn't inherit.
var AgentVars = []string{
	"PI_CODING_AGENT_DIR",
	"CODEX_HOME",
	"CLAUDE_CONFIG_DIR",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_BASE_URL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"GEMINI_API_KEY",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"HTTP_PROXY",
	"http_proxy",
	"HTTPS_PROXY",
	"https_proxy",
	"ALL_PROXY",
	"all_proxy",
	"NO_PROXY",
	"no_proxy",
}

const (
	shellWait = 2 * time.Second
	shellAsk  = 5 * time.Second
	shellMark = "__loom_path__"
)

// UserPath populates the desktop app's PATH and agent environment variables
// from known macOS tool directories and the user's login shell.
func UserPath() {
	addPath(userDirs())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p, vars := shellEnv()
		setUnset(vars)
		if p != "" {
			addPath(filepath.SplitList(p))
		}
	}()
	select {
	case <-done:
	case <-time.After(shellWait):
	}
}

func addPath(dirs []string) {
	if len(dirs) == 0 {
		return
	}
	cur := filepath.SplitList(os.Getenv("PATH"))
	var merged []string
	for _, d := range append(dirs, cur...) {
		if d != "" && !slices.Contains(merged, d) {
			merged = append(merged, d)
		}
	}
	_ = os.Setenv("PATH", strings.Join(merged, string(os.PathListSeparator)))
}

func setUnset(vars map[string]string) {
	for k, v := range vars {
		if _, set := os.LookupEnv(k); !set && v != "" {
			_ = os.Setenv(k, v)
		}
	}
}

func userDirs() []string {
	home, _ := os.UserHomeDir()
	var known []string
	if home != "" {
		for _, d := range []string{
			".local/bin",
			".pi/agent/bin",
			".npm-global/bin",
			".npm/bin",
			".bun/bin",
			".volta/bin",
			".asdf/shims",
			".local/share/mise/shims",
			".cargo/bin",
			".deno/bin",
			"Library/pnpm",
			".local/share/pnpm",
		} {
			known = append(known, filepath.Join(home, d))
		}
		if p := npmPrefix(home); p != "" {
			known = append(known, p)
		}
		known = append(known, nvmBins(home)...)
	}
	known = append(known, "/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin")
	var have []string
	for _, d := range known {
		if st, err := os.Stat(d); d != "" && err == nil && st.IsDir() {
			have = append(have, d)
		}
	}
	return have
}

func npmPrefix(home string) string {
	b, err := os.ReadFile(filepath.Join(home, ".npmrc"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "prefix" {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if strings.HasPrefix(v, "~/") {
			v = filepath.Join(home, v[2:])
		}
		if filepath.IsAbs(v) {
			return filepath.Join(v, "bin")
		}
	}
	return ""
}

func nvmBins(home string) []string {
	nvm := os.Getenv("NVM_DIR")
	if !filepath.IsAbs(nvm) {
		nvm = filepath.Join(home, ".nvm")
	}
	entries, err := os.ReadDir(filepath.Join(nvm, "versions", "node"))
	if err != nil {
		return nil
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].IsDir() {
			out = append(out, filepath.Join(nvm, "versions", "node", entries[i].Name(), "bin"))
		}
	}
	return out
}

func shellEnv() (string, map[string]string) {
	sh := os.Getenv("SHELL")
	if sh == "" || !filepath.IsAbs(sh) {
		sh = "/bin/zsh"
		if _, err := os.Stat(sh); err != nil {
			sh = "/bin/sh"
		}
	}
	var wg sync.WaitGroup
	var p string
	var vars map[string]string
	wg.Add(1)
	go func() {
		defer wg.Done()
		p, vars = askShell(sh)
	}()
	wg.Wait()
	return p, vars
}

func askShell(sh string) (string, map[string]string) {
	for _, flags := range []string{"-ilc", "-lc"} {
		if p, vars := askShellAs(sh, flags); p != "" || vars != nil {
			return p, vars
		}
	}
	return "", nil
}

func askShellAs(sh, flags string) (string, map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), shellAsk)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, flags, shellProbe(sh))
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.Env = append(os.Environ(), "LOOM_SHELL_PROBE=1", "MAGPIE_SHELL_PROBE=1")
	out, _ := cmd.Output()
	return parseShellEnv(string(out))
}

func shellProbe(sh string) string {
	format, args := shellMark+"%s", ` "$PATH"`
	for _, v := range AgentVars {
		format += `\0%s`
		args += ` "$` + v + `"`
	}
	cmd := `/bin/sh -c 'printf "` + format + shellMark + `"` + args + `'`
	if filepath.Base(sh) == "nu" {
		return "^" + cmd
	}
	return cmd
}

func parseShellEnv(s string) (string, map[string]string) {
	i := strings.Index(s, shellMark)
	if i < 0 {
		return "", nil
	}
	s = s[i+len(shellMark):]
	j := strings.Index(s, shellMark)
	if j < 0 {
		return "", nil
	}
	f := strings.Split(s[:j], "\x00")
	if f[0] == "$PATH" {
		f[0] = ""
	}
	if len(f) != len(AgentVars)+1 {
		if len(f) == 1 {
			return f[0], nil
		}
		return "", nil
	}
	vars := make(map[string]string, len(AgentVars))
	for k, v := range AgentVars {
		if f[k+1] != "$"+v {
			vars[v] = f[k+1]
		}
	}
	return f[0], vars
}
