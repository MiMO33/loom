package kanban

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

func shaHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func Git(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	full := append([]string{"-C", cwd}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\r\n "), nil
}

func gitChanges(root string) (map[string]string, error) {
	raw, err := Git(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(raw, "\x00")
	result := make(map[string]string)

	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		code := entry[:2]
		p := entry[3:]
		content := []byte("deleted")
		full := filepath.Join(root, p)
		if st, err := os.Lstat(full); err == nil {
			switch {
			case st.Mode()&os.ModeSymlink != 0:
				if target, err := os.Readlink(full); err == nil {
					content = []byte(target)
				}
			case st.Mode().IsRegular():
				if b, err := os.ReadFile(full); err == nil {
					content = b
				}
			default:
				content = []byte("directory")
			}
		}
		staged, _ := Git(root, "diff", "--cached", "--binary", "--", p)
		result[p] = shaHex([]byte(code + "\x00" + shaHex(content) + "\x00" + staged))

		if strings.ContainsAny(code, "RC") && i+1 < len(parts) {
			i++
			orig := parts[i]
			if orig != "" {
				result[orig] = shaHex([]byte("rename:" + p))
			}
		}
	}
	return result, nil
}

func Snapshot(cwd string) *GitSnapshot {
	root, err := Git(cwd, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return nil
	}
	head, _ := Git(root, "rev-parse", "--verify", "HEAD")
	branch, _ := Git(root, "symbolic-ref", "--short", "HEAD")
	dirty, err := gitChanges(root)
	if err != nil {
		return nil
	}
	return &GitSnapshot{
		Root:   root,
		Branch: branch,
		Head:   head,
		Dirty:  dirty,
	}
}

func Review(task *Task) (*GitReview, error) {
	if task.Baseline == nil {
		return &GitReview{
			Available: false,
			Diff:      "",
			Files:     []string{},
			Excluded:  []string{},
			Token:     shaHex([]byte(task.ID + ":no-git")),
		}, nil
	}

	root := task.Baseline.Root
	branch := task.Baseline.Branch
	dirty := task.Baseline.Dirty
	if dirty == nil {
		dirty = map[string]string{}
	}

	current := Snapshot(root)
	if current == nil {
		return nil, errors.New("项目 Git 仓库已不可用")
	}

	files := []string{}
	excluded := []string{}
	for p := range current.Dirty {
		if _, wasDirty := dirty[p]; !wasDirty {
			files = append(files, p)
		} else {
			excluded = append(excluded, p)
		}
	}
	slices.Sort(files)
	slices.Sort(excluded)

	var overlap []string
	for p, oldHash := range dirty {
		if curHash, ok := current.Dirty[p]; ok && curHash != oldHash {
			overlap = append(overlap, p)
		}
	}
	slices.Sort(overlap)

	var blocked string
	if current.Branch != branch {
		blocked = "项目分支已切换，请先恢复任务开始时的分支：" + branch
	} else if current.Branch == "" {
		blocked = "当前为 detached HEAD，请先在项目中检出任务分支。"
	} else if len(overlap) > 0 {
		blocked = "以下文件同时包含运行前的改动和本次改动，请先在外部核对并提交：" + strings.Join(overlap, "、")
	}

	var trackedDiff string
	if current.Head != "" && len(files) > 0 {
		args := append([]string{"diff", "--binary", current.Head, "--"}, files...)
		trackedDiff, _ = Git(root, args...)
	}

	lsRaw, _ := Git(root, "ls-files", "-z")
	tracked := make(map[string]bool)
	for _, f := range strings.Split(lsRaw, "\x00") {
		if f != "" {
			tracked[f] = true
		}
	}

	var untracked []string
	for _, p := range files {
		if tracked[p] {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
			preview := string(b)
			if bytes.IndexByte(b, 0) >= 0 {
				preview = "[二进制文件]"
			} else if len(preview) > 32000 {
				preview = preview[:32000]
			}
			untracked = append(untracked, "\n新增文件："+p+"\n"+preview)
		}
	}

	diff := trackedDiff + strings.Join(untracked, "\n")
	if len(diff) > 500000 {
		diff = diff[:500000]
	}

	tokenPayload, _ := json.Marshal(map[string]any{
		"head":   current.Head,
		"branch": current.Branch,
		"dirty":  current.Dirty,
	})

	return &GitReview{
		Available: true,
		Root:      root,
		Branch:    current.Branch,
		Head:      current.Head,
		Files:     files,
		Excluded:  excluded,
		Blocked:   blocked,
		Diff:      diff,
		Token:     shaHex(tokenPayload),
	}, nil
}

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):`)

func commitURL(remote, commit string) string {
	u := sshRemoteRe.ReplaceAllString(remote, "https://$1/")
	u = strings.TrimSuffix(u, ".git")
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u + "/commit/" + commit
	}
	return ""
}

func CommitTask(task *Task, expectedToken, messagePath string) (*GitResult, error) {
	info, err := Review(task)
	if err != nil {
		return nil, err
	}
	automatic := task.Completion != nil && task.Completion.Automatic
	if !info.Available {
		return &GitResult{Status: "unavailable", Automatic: automatic, Message: "工作目录未使用 Git"}, nil
	}
	if info.Blocked != "" {
		return nil, errors.New(info.Blocked)
	}
	if expectedToken != "" && info.Token != expectedToken {
		return nil, Fail("diff 已更新，请重新查看后确认。", http.StatusConflict)
	}

	if len(info.Files) == 0 {
		if info.Head != "" && (task.Baseline == nil || info.Head != task.Baseline.Head) {
			lastMsg, err := Git(info.Root, "show", "-s", "--format=%B", info.Head)
			if err == nil && (strings.Contains(lastMsg, "Agent-Kanban-Task: "+task.ID) || strings.Contains(lastMsg, "Loom-Task: "+task.ID)) {
				remote, _ := Git(info.Root, "remote", "get-url", "origin")
				return &GitResult{
					Status:    "committed",
					Automatic: strings.Contains(lastMsg, "Commit-Mode: automatic"),
					Hash:      info.Head,
					Message:   lastMsg,
					URL:       commitURL(remote, info.Head),
				}, nil
			}
		}
		return &GitResult{Status: "unchanged", Automatic: automatic, Message: "没有需要提交的文件"}, nil
	}

	root := info.Root
	msg := fmt.Sprintf("feat: task %s\n\n- 完成任务 %s 的改动。", task.Title, task.ID)
	if task.Completion != nil && strings.TrimSpace(task.Completion.Message) != "" {
		msg = task.Completion.Message
	}
	modeStr := "manual"
	if automatic {
		modeStr = "automatic"
	}
	trailer := fmt.Sprintf("\n\nAgent-Kanban-Task: %s\nAgent-Kanban-Commit-Mode: %s\n", task.ID, modeStr)
	if err := os.WriteFile(messagePath, []byte(msg+trailer), 0o600); err != nil {
		return nil, err
	}

	addArgs := append([]string{"add", "--"}, info.Files...)
	if _, err := Git(root, addArgs...); err != nil {
		return nil, err
	}
	commitArgs := append([]string{"commit", "--only", "-F", messagePath, "--"}, info.Files...)
	if _, err := Git(root, commitArgs...); err != nil {
		if info.Head != "" {
			resetArgs := append([]string{"reset", "-q", info.Head, "--"}, info.Files...)
			_, _ = Git(root, resetArgs...)
		}
		return nil, err
	}

	commit, err := Git(root, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	actualMsg, _ := Git(root, "show", "-s", "--format=%B", commit)
	remote, _ := Git(root, "remote", "get-url", "origin")
	return &GitResult{
		Status:    "committed",
		Automatic: automatic,
		Hash:      commit,
		Message:   actualMsg,
		URL:       commitURL(remote, commit),
	}, nil
}

func CreatePR(task *Task, bodyPath string) (string, error) {
	root := task.Baseline.Root
	runGh := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gh", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				return "", fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
			}
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}

	if existing, err := runGh("pr", "view", "--json", "url", "--jq", ".url"); err == nil && existing != "" {
		return existing, nil
	}

	branch, err := Git(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	base, err := runGh("repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")
	if err != nil {
		return "", err
	}
	if branch == base {
		return "", errors.New("创建 PR 需要任务分支；当前分支为 " + base)
	}
	hash := "无新提交"
	if task.Git != nil && task.Git.Hash != "" {
		hash = task.Git.Hash
	}
	body := fmt.Sprintf("1. task %s\n\n%s\n\n提交：%s\n", task.Title, task.Description, hash)
	if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
		return "", err
	}
	if _, err := Git(root, "push", "-u", "origin", branch); err != nil {
		return "", err
	}
	return runGh("pr", "create", "--base", base, "--head", branch, "--title", task.Title, "--body-file", bodyPath)
}
