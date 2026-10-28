package kanban

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

func Fail(message string, status int) error {
	if status == 0 {
		status = http.StatusBadRequest
	}
	return &StatusError{Status: status, Message: message}
}

type Database struct {
	Version        int      `json:"version"`
	Revision       int      `json:"revision"`
	LastProjectDir string   `json:"lastProjectDir,omitempty"`
	ProjectDirs    []string `json:"projectDirs,omitempty"`
	Tasks          []*Task  `json:"tasks"`
}

type Store struct {
	Root string
	Data Database
}

// DefaultDataDir returns ~/.loom (or LOOM_DATA_DIR / AGENT_KANBAN_DATA_DIR),
// migrating existing ~/.agent-kanban data on first run if ~/.loom/db.json is absent.
func DefaultDataDir() string {
	if d := os.Getenv("LOOM_DATA_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("AGENT_KANBAN_DATA_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	loomDir := filepath.Join(home, ".loom")
	legacyDir := filepath.Join(home, ".agent-kanban")
	if _, err := os.Stat(filepath.Join(loomDir, "db.json")); errors.Is(err, os.ErrNotExist) {
		if _, lerr := os.Stat(filepath.Join(legacyDir, "db.json")); lerr == nil {
			_ = migrateLegacyDir(legacyDir, loomDir)
		}
	}
	return loomDir
}

func migrateLegacyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "server.lock" {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if rel == "db.json" {
			b = []byte(strings.ReplaceAll(string(b), src, dst))
		}
		return os.WriteFile(target, b, 0o600)
	})
}

func NewStore(root string) (*Store, error) {
	for _, sub := range []string{"", "tasks", "archive"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return nil, err
		}
	}
	s := &Store{Root: root}
	dbPath := filepath.Join(root, "db.json")
	if b, err := os.ReadFile(dbPath); err == nil {
		if err := json.Unmarshal(b, &s.Data); err != nil || s.Data.Version != 1 || s.Data.Tasks == nil {
			return nil, errors.New("db.json 格式不兼容，请保留原文件后检查。")
		}
		if s.Data.ProjectDirs == nil {
			s.Data.ProjectDirs = []string{}
		}
		cleanDirs := []string{}
		for _, dir := range s.Data.ProjectDirs {
			dir = strings.TrimSpace(dir)
			if dir == "" {
				continue
			}
			expanded := Expand(dir)
			if inside(root, expanded) {
				continue
			}
			if !slices.Contains(cleanDirs, expanded) {
				cleanDirs = append(cleanDirs, expanded)
			}
		}
		s.Data.ProjectDirs = cleanDirs
		if s.Data.LastProjectDir != "" {
			expandedLast := Expand(strings.TrimSpace(s.Data.LastProjectDir))
			if inside(root, expandedLast) {
				s.Data.LastProjectDir = ""
			} else {
				s.Data.LastProjectDir = expandedLast
				if !slices.Contains(s.Data.ProjectDirs, expandedLast) {
					s.Data.ProjectDirs = append([]string{expandedLast}, s.Data.ProjectDirs...)
				}
			}
		}
		healed := false
		for _, t := range s.Data.Tasks {
			if t.ProjectDir != "" {
				expanded := Expand(strings.TrimSpace(t.ProjectDir))
				if inside(root, expanded) {
					t.ProjectDir = ""
				} else {
					t.ProjectDir = expanded
					if !slices.Contains(s.Data.ProjectDirs, expanded) {
						s.Data.ProjectDirs = append(s.Data.ProjectDirs, expanded)
					}
				}
			}
			if t.Input == nil {
				t.Input = []TaskFile{}
			}
			if t.Output == nil {
				t.Output = []TaskFile{}
			}
			if t.Sessions == nil {
				t.Sessions = []*Session{}
			}
			for _, sess := range t.Sessions {
				if sess.Messages == nil {
					sess.Messages = []Message{}
				}
				if sess.RecentEvents == nil {
					sess.RecentEvents = []string{}
				}
				if sess.Status == SessionEnded && sess.Error == "Agent 报告执行失败" {
					logPath := filepath.Join(root, "tasks", t.ID, "logs", sess.ID+".log")
					if sessionResultSucceeded(logPath) {
						zero := 0
						sess.ExitCode = &zero
						sess.Error = ""
						if t.Status == StatusWaiting && t.OneShot {
							t.OneShot = false
						}
						resPath := filepath.Join(root, "tasks", t.ID, "runtime", sess.ID, sess.RunID, "result.json")
						if rb, err := os.ReadFile(resPath); err == nil {
							var resObj map[string]any
							if json.Unmarshal(rb, &resObj) == nil {
								resObj["exitCode"] = 0
								delete(resObj, "error")
								if out, err := json.Marshal(resObj); err == nil {
									_ = os.WriteFile(resPath, out, 0o600)
								}
							}
						}
						healed = true
					}
				}
			}
		}
		if s.Data.LastProjectDir == "" && len(s.Data.ProjectDirs) > 0 {
			s.Data.LastProjectDir = s.Data.ProjectDirs[0]
		}
		for _, t := range s.Data.Tasks {
			if t.ProjectDir == "" && t.Status != StatusDone && s.Data.LastProjectDir != "" {
				t.ProjectDir = s.Data.LastProjectDir
			}
		}
		if healed {
			s.Save()
		}
	} else if errors.Is(err, os.ErrNotExist) {
		s.Data = Database{Version: 1, Revision: 0, ProjectDirs: []string{}, Tasks: []*Task{}}
		s.Save()
	} else {
		return nil, err
	}
	return s, nil
}

func (s *Store) RememberProjectDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	dir = Expand(dir)
	if inside(s.Root, dir) {
		return
	}
	s.Data.LastProjectDir = dir
	filtered := []string{dir}
	for _, existing := range s.Data.ProjectDirs {
		if existing != "" && existing != dir {
			filtered = append(filtered, existing)
		}
	}
	if len(filtered) > 20 {
		filtered = filtered[:20]
	}
	s.Data.ProjectDirs = filtered
}

func (s *Store) RemoveProjectDir(dir string) {
	dir = strings.TrimSpace(dir)
	expanded := dir
	if dir != "" {
		expanded = Expand(dir)
	}
	filtered := []string{}
	for _, existing := range s.Data.ProjectDirs {
		if existing != "" && existing != dir && existing != expanded {
			filtered = append(filtered, existing)
		}
	}
	s.Data.ProjectDirs = filtered
	if s.Data.LastProjectDir == dir || s.Data.LastProjectDir == expanded {
		if len(filtered) > 0 {
			s.Data.LastProjectDir = filtered[0]
		} else {
			s.Data.LastProjectDir = ""
		}
	}
	s.Save()
}

func (s *Store) Task(id string) (*Task, error) {
	for _, t := range s.Data.Tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, Fail("任务不存在", http.StatusNotFound)
}

func (s *Store) Dir(id string) (string, error) {
	t, err := s.Task(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "tasks", t.ID), nil
}

func (s *Store) Save() {
	s.Data.Revision++
	tmp := filepath.Join(s.Root, "db.json.tmp")
	b, _ := json.MarshalIndent(s.Data, "", "  ")
	_ = os.WriteFile(tmp, b, 0o600)
	_ = os.Chmod(tmp, 0o600)
	_ = os.Rename(tmp, filepath.Join(s.Root, "db.json"))
}

func (s *Store) Touch(task *Task) {
	task.UpdatedAt = NowISO()
	s.Save()
}

func sessionResultSucceeded(logPath string) bool {
	tail := Tail(logPath, 64*1024)
	if tail == "" {
		return false
	}
	lines := strings.Split(strings.TrimSpace(tail), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			continue
		}
		if typ, _ := raw["type"].(string); typ == "result" {
			isErr, _ := raw["is_error"].(bool)
			subtype, _ := raw["subtype"].(string)
			return !isErr && !strings.HasPrefix(subtype, "error")
		}
	}
	return false
}

func NowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
