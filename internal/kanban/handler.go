package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type DesktopBridge interface {
	ChooseFolder(title string) (string, error)
	OpenFolder(path string) error
	OpenURL(url string) error
	Copy(text string) bool
}

type ownWindowKey struct{}

// OwnWindow marks requests coming directly from the Wails desktop window.
func OwnWindow(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = "127.0.0.1:0"
		r = r.WithContext(context.WithValue(r.Context(), ownWindowKey{}, true))
		next.ServeHTTP(w, r)
	})
}

var loopbackHostRe = regexp.MustCompile(`^(127\.0\.0\.1|localhost|wails\.localhost)(:\d+)?$`)
var safeExtRe = regexp.MustCompile(`[^.a-zA-Z0-9]`)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var se *StatusError
	if errors.As(err, &se) {
		writeJSON(w, se.Status, map[string]string{"error": se.Message})
		return
	}
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func decodeBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
	if err != nil {
		return Fail("读取请求失败", http.StatusBadRequest)
	}
	if len(b) > 2*1024*1024 {
		return Fail("请求体过大", http.StatusBadRequest)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return Fail("请求格式错误："+err.Error(), http.StatusBadRequest)
	}
	return nil
}

func validateTool(t Tool) bool {
	return slices.Contains(ValidTools, t)
}

func validateModeValue(m Mode) bool {
	return m == ModeHeadless || m == ModeTmux
}

func NewAPIHandler(manager *Manager, desktop DesktopBridge) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/board", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		board := Board{
			Tasks:          manager.Store.Data.Tasks,
			Revision:       manager.Store.Data.Revision,
			LastProjectDir: manager.Store.Data.LastProjectDir,
			ProjectDirs:    manager.Store.Data.ProjectDirs,
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, board)
		manager.Unlock()
	})

	mux.HandleFunc("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		p := strings.TrimSpace(in.Path)
		if p == "" {
			writeErr(w, Fail("请选择或输入项目目录。", http.StatusBadRequest))
			return
		}
		expanded := Expand(p)
		if inside(manager.Store.Root, expanded) {
			writeErr(w, Fail("项目目录不能位于 .loom 数据目录内。", http.StatusBadRequest))
			return
		}
		if st, err := os.Stat(expanded); err != nil || !st.IsDir() {
			writeErr(w, Fail("项目目录不存在："+expanded, http.StatusBadRequest))
			return
		}
		manager.Lock()
		manager.Store.RememberProjectDir(expanded)
		manager.Store.Save()
		res := map[string]any{
			"lastProjectDir": manager.Store.Data.LastProjectDir,
			"projectDirs":    manager.Store.Data.ProjectDirs,
		}
		manager.Unlock()
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("DELETE /api/projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		manager.Lock()
		manager.Store.RemoveProjectDir(in.Path)
		res := map[string]any{
			"lastProjectDir": manager.Store.Data.LastProjectDir,
			"projectDirs":    manager.Store.Data.ProjectDirs,
		}
		manager.Unlock()
		writeJSON(w, http.StatusOK, res)
	})

	var (
		healthMu     sync.Mutex
		cachedHealth *Health
	)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		refresh := r.URL.Query().Get("refresh") != ""
		healthMu.Lock()
		h := cachedHealth
		healthMu.Unlock()
		if h == nil || refresh {
			names := []string{"claude", "codex", "pi", "gemini", "opencode", "cursor", "tmux", "git", "gh"}
			tools := make(map[string]ToolHealth, len(names))
			var wg sync.WaitGroup
			var mu sync.Mutex
			for _, name := range names {
				wg.Add(1)
				go func(n string) {
					defer wg.Done()
					cmd := n
					if custom := manager.Options.Executables[Tool(n)]; custom != "" {
						cmd = custom
					}
					th := CheckToolHealth(cmd)
					mu.Lock()
					tools[n] = th
					mu.Unlock()
				}(name)
			}
			wg.Wait()
			cwd, _ := os.Getwd()
			h = &Health{
				Tools:      tools,
				DataDir:    manager.Options.DataDir,
				ProjectDir: cwd,
			}
			healthMu.Lock()
			cachedHealth = h
			healthMu.Unlock()
		}
		writeJSON(w, http.StatusOK, h)
	})

	mux.HandleFunc("POST /api/tasks", func(w http.ResponseWriter, r *http.Request) {
		in := struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Tool        Tool   `json:"tool"`
			Mode        Mode   `json:"mode"`
			OneShot     *bool  `json:"oneShot"`
			ProjectDir  string `json:"projectDir"`
			ArchivePath string `json:"archivePath"`
		}{
			Tool:        ToolClaude,
			Mode:        ModeHeadless,
			ArchivePath: DefaultArchivePath,
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			writeErr(w, Fail("请输入任务名", http.StatusBadRequest))
			return
		}
		if len([]rune(title)) > 200 || len(in.Description) > 200000 {
			writeErr(w, Fail("任务内容超出长度限制", http.StatusBadRequest))
			return
		}
		if !validateTool(in.Tool) || !validateModeValue(in.Mode) {
			writeErr(w, Fail("工具或运行模式无效", http.StatusBadRequest))
			return
		}
		oneShot := true
		if in.OneShot != nil {
			oneShot = *in.OneShot
		}
		task, err := manager.Create(DraftInput{
			Title:       title,
			Description: in.Description,
			Tool:        in.Tool,
			Mode:        in.Mode,
			OneShot:     oneShot,
			ProjectDir:  strings.TrimSpace(in.ProjectDir),
			ArchivePath: strings.TrimSpace(in.ArchivePath),
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, task)
	})

	mux.HandleFunc("PATCH /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var raw map[string]json.RawMessage
		if err := decodeBody(r, &raw); err != nil {
			writeErr(w, err)
			return
		}
		var patch PatchInput
		if v, ok := raw["title"]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil || strings.TrimSpace(s) == "" || len([]rune(strings.TrimSpace(s))) > 200 {
				writeErr(w, Fail("请输入任务名", http.StatusBadRequest))
				return
			}
			s = strings.TrimSpace(s)
			patch.Title = &s
		}
		if v, ok := raw["description"]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil || len(s) > 200000 {
				writeErr(w, Fail("需求内容无效", http.StatusBadRequest))
				return
			}
			patch.Description = &s
		}
		if v, ok := raw["tool"]; ok {
			var t Tool
			if json.Unmarshal(v, &t) != nil || !validateTool(t) {
				writeErr(w, Fail("工具无效", http.StatusBadRequest))
				return
			}
			patch.Tool = &t
		}
		if v, ok := raw["mode"]; ok {
			var m Mode
			if json.Unmarshal(v, &m) != nil || !validateModeValue(m) {
				writeErr(w, Fail("模式无效", http.StatusBadRequest))
				return
			}
			patch.Mode = &m
		}
		if v, ok := raw["oneShot"]; ok {
			var b bool
			if json.Unmarshal(v, &b) != nil {
				writeErr(w, Fail("参数无效", http.StatusBadRequest))
				return
			}
			patch.OneShot = &b
		}
		if v, ok := raw["projectDir"]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil {
				writeErr(w, Fail("项目目录无效", http.StatusBadRequest))
				return
			}
			s = strings.TrimSpace(s)
			patch.ProjectDir = &s
		}
		if v, ok := raw["archivePath"]; ok {
			var s string
			if json.Unmarshal(v, &s) != nil {
				writeErr(w, Fail("归档目录无效", http.StatusBadRequest))
				return
			}
			s = strings.TrimSpace(s)
			patch.ArchivePath = &s
		}
		if v, ok := raw["archived"]; ok {
			var b bool
			if json.Unmarshal(v, &b) != nil {
				writeErr(w, Fail("参数无效", http.StatusBadRequest))
				return
			}
			patch.Archived = &b
		}
		updated, err := manager.Edit(task, patch)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})

	mux.HandleFunc("DELETE /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := manager.Delete(task); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
	})

	mux.HandleFunc("POST /api/tasks/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		if task.Status != StatusTodo && task.Status != StatusWaiting {
			manager.Unlock()
			writeErr(w, Fail("请在任务停止后上传输入文件。", http.StatusConflict))
			return
		}
		taskDir := filepath.Join(manager.Store.Root, "tasks", task.ID)
		manager.Unlock()

		r.Body = http.MaxBytesReader(w, r.Body, 20*20*1024*1024+1024*1024)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeErr(w, Fail("上传失败：最多 20 个文件，每个不超过 20 MB。", http.StatusBadRequest))
			return
		}
		fhs := r.MultipartForm.File["files"]
		if len(fhs) > 20 {
			writeErr(w, Fail("上传失败：最多 20 个文件，每个不超过 20 MB。", http.StatusBadRequest))
			return
		}
		var uploaded []TaskFile
		for _, fh := range fhs {
			if fh.Size > 20*1024*1024 {
				writeErr(w, Fail("上传失败：最多 20 个文件，每个不超过 20 MB。", http.StatusBadRequest))
				return
			}
			f, err := fh.Open()
			if err != nil {
				writeErr(w, err)
				return
			}
			data, err := io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				writeErr(w, err)
				return
			}
			id := uuid.NewString()
			name := filepath.Base(fh.Filename)
			ext := safeExtRe.ReplaceAllString(filepath.Ext(name), "")
			if len(ext) > 12 {
				ext = ext[:12]
			}
			dst := filepath.Join(taskDir, "input", id+ext)
			if err := os.WriteFile(dst, data, 0o600); err != nil {
				writeErr(w, err)
				return
			}
			uploaded = append(uploaded, TaskFile{
				ID:   id,
				Name: name,
				Path: dst,
				Size: int64(len(data)),
				Mime: fh.Header.Get("Content-Type"),
			})
		}
		manager.Lock()
		task.Input = append(task.Input, uploaded...)
		manager.Store.Touch(task)
		manager.Unlock()
		writeJSON(w, http.StatusCreated, uploaded)
	})

	mux.HandleFunc("DELETE /api/tasks/{id}/files/{fileId}", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		defer manager.Unlock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if task.Status != StatusTodo && task.Status != StatusWaiting {
			writeErr(w, Fail("请在任务停止后移除输入文件。", http.StatusConflict))
			return
		}
		fid := r.PathValue("fileId")
		filtered := make([]TaskFile, 0, len(task.Input))
		for _, f := range task.Input {
			if f.ID != fid {
				filtered = append(filtered, f)
			}
		}
		task.Input = filtered
		manager.Store.Touch(task)
		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("GET /api/tasks/{id}/files/{fileId}", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		f, err := manager.File(task, r.PathValue("fileId"))
		if err != nil {
			writeErr(w, err)
			return
		}
		ext := strings.ToLower(filepath.Ext(f.Path))
		imageTypes := map[string]string{
			".png":  "image/png",
			".jpg":  "image/jpeg",
			".jpeg": "image/jpeg",
			".gif":  "image/gif",
			".webp": "image/webp",
		}
		if mimeType, ok := imageTypes[ext]; ok {
			w.Header().Set("Content-Type", mimeType)
			http.ServeFile(w, r, f.Path)
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.Name))
		http.ServeFile(w, r, f.Path)
	})

	mux.HandleFunc("POST /api/tasks/{id}/outputs", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		if task.Status == StatusDone {
			manager.Unlock()
			writeErr(w, Fail("任务已完成。", http.StatusConflict))
			return
		}
		manager.Unlock()

		var in struct {
			Paths []string `json:"paths"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if len(in.Paths) == 0 || len(in.Paths) > 100 {
			writeErr(w, Fail("输出路径数量无效", http.StatusBadRequest))
			return
		}
		if err := manager.Report(task, in.Paths); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("POST /api/tasks/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			Tool       Tool   `json:"tool"`
			Mode       Mode   `json:"mode"`
			ProjectDir string `json:"projectDir"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Tool != "" && !validateTool(in.Tool) {
			writeErr(w, Fail("工具无效", http.StatusBadRequest))
			return
		}
		if in.Mode != "" && !validateModeValue(in.Mode) {
			writeErr(w, Fail("模式无效", http.StatusBadRequest))
			return
		}
		if p := strings.TrimSpace(in.ProjectDir); p != "" {
			expanded := Expand(p)
			if inside(manager.Store.Root, expanded) {
				writeErr(w, Fail("项目目录不能位于 .loom 数据目录内。", http.StatusBadRequest))
				return
			}
			if st, err := os.Stat(expanded); err != nil || !st.IsDir() {
				writeErr(w, Fail("项目目录不存在："+expanded, http.StatusBadRequest))
				return
			}
			manager.Lock()
			if task.ProjectDir != expanded {
				task.ProjectDir = expanded
				task.Baseline = nil
			}
			manager.Store.RememberProjectDir(expanded)
			manager.Store.Touch(task)
			manager.Unlock()
		}
		session, err := manager.Launch(task, in.Tool, in.Mode, "")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, session)
	})

	mux.HandleFunc("POST /api/tasks/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			Status TaskStatus `json:"status"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if in.Status == task.Status {
			writeJSON(w, http.StatusOK, task)
			return
		}
		switch {
		case in.Status == StatusDoing && task.Status == StatusTodo:
			if _, err := manager.Launch(task, "", "", ""); err != nil {
				writeErr(w, err)
				return
			}
		case in.Status == StatusDoing && task.Status == StatusWaiting:
			var lastWork *Session
			for _, s := range task.Sessions {
				if s.Kind == "work" {
					lastWork = s
				}
			}
			if lastWork != nil && lastWork.NativeID != "" {
				if _, err := manager.Chat(task, lastWork, "继续本任务，请根据此前结果说明下一步，等待用户补充要求。", ""); err != nil {
					writeErr(w, err)
					return
				}
			} else {
				if _, err := manager.Launch(task, "", "", ""); err != nil {
					writeErr(w, err)
					return
				}
			}
		case in.Status == StatusDone && task.Status == StatusWaiting:
			rev, err := manager.GitReview(task)
			if err != nil {
				writeErr(w, err)
				return
			}
			if rev.Blocked != "" {
				writeErr(w, Fail(rev.Blocked, http.StatusConflict))
				return
			}
			if rev.Available && len(rev.Files) > 0 {
				writeErr(w, Fail("请在详情中查看 diff，然后确认完成。", http.StatusConflict))
				return
			}
			msg := fmt.Sprintf("feat: task %s\n\n- 完成任务 %s 的改动。", task.Title, task.ID)
			if err := manager.Finish(task, false, "commit", msg, rev.Token); err != nil {
				writeErr(w, err)
				return
			}
		case in.Status == StatusDone:
			writeErr(w, Fail("请在详情中查看 diff，然后确认完成。", http.StatusConflict))
			return
		default:
			writeErr(w, Fail("当前 session 状态不允许移动到此列。", http.StatusConflict))
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	findSession := func(task *Task, sid string) (*Session, error) {
		for _, s := range task.Sessions {
			if s.ID == sid {
				return s, nil
			}
		}
		return nil, Fail("Session 不存在", http.StatusNotFound)
	}

	mux.HandleFunc("GET /api/tasks/{id}/sessions/{sid}/log", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		session, err := findSession(task, r.PathValue("sid"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		logPath := manager.LogPath(task, session)
		manager.Unlock()

		st, statErr := os.Stat(logPath)
		truncated := statErr == nil && st.Size() > 160000
		writeJSON(w, http.StatusOK, map[string]any{
			"text":      Tail(logPath, 160000),
			"truncated": truncated,
		})
	})

	mux.HandleFunc("POST /api/tasks/{id}/sessions/{sid}/chat", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		session, err := findSession(task, r.PathValue("sid"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			Message string `json:"message"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		msg := strings.TrimSpace(in.Message)
		if msg == "" || len(msg) > 64000 {
			writeErr(w, Fail("请输入有效消息", http.StatusBadRequest))
			return
		}
		updated, err := manager.Chat(task, session, msg, "")
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})

	mux.HandleFunc("POST /api/tasks/{id}/sessions/{sid}/attach", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		session, err := findSession(task, r.PathValue("sid"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		in := struct {
			OpenTerminal *bool `json:"openTerminal"`
		}{}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		openTerm := true
		if in.OpenTerminal != nil {
			openTerm = *in.OpenTerminal
		}
		res, err := manager.Attach(task, session, openTerm)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("POST /api/tasks/{id}/sessions/{sid}/stop", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		if err != nil {
			manager.Unlock()
			writeErr(w, err)
			return
		}
		session, err := findSession(task, r.PathValue("sid"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := manager.Stop(task, session); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("GET /api/tasks/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		rev, err := manager.GitReview(task)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rev)
	})

	mux.HandleFunc("POST /api/tasks/{id}/complete", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		in := struct {
			Token   string `json:"token"`
			Action  string `json:"action"`
			Message string `json:"message"`
		}{Action: "commit"}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		in.Message = strings.TrimSpace(in.Message)
		if in.Token == "" || in.Message == "" || (in.Action != "commit" && in.Action != "pr") {
			writeErr(w, Fail("提交参数无效", http.StatusBadRequest))
			return
		}
		if err := manager.Finish(task, false, in.Action, in.Message, in.Token); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("POST /api/tasks/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := manager.Archive(task); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	})

	mux.HandleFunc("GET /api/tasks/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		if task.Archive.Path == "" {
			writeErr(w, Fail("归档文件尚未生成。", http.StatusNotFound))
			return
		}
		if _, err := os.Stat(task.Archive.Path); err != nil {
			writeErr(w, Fail("归档文件尚未生成。", http.StatusNotFound))
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(task.Archive.Path)))
		http.ServeFile(w, r, task.Archive.Path)
	})

	mux.HandleFunc("POST /api/tasks/{id}/open", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			FileID  string `json:"fileId"`
			Archive bool   `json:"archive"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		var target string
		if in.Archive {
			target = task.Archive.Path
		} else if in.FileID != "" {
			f, err := manager.File(task, in.FileID)
			if err != nil {
				writeErr(w, err)
				return
			}
			target = f.Path
		}
		if target == "" {
			writeErr(w, Fail("文件不存在", http.StatusNotFound))
			return
		}
		if _, err := os.Stat(target); err != nil {
			writeErr(w, Fail("文件不存在", http.StatusNotFound))
			return
		}
		if err := exec.Command("open", target).Run(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无法打开文件：" + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"opened": true})
	})

	resolveTaskMarkdownFile := func(task *Task, fileID string, isArchive bool) (string, string, error) {
		if isArchive {
			if task.Archive.Path == "" {
				return "", "", Fail("归档文件尚未生成。", http.StatusNotFound)
			}
			abs, err := filepath.EvalSymlinks(task.Archive.Path)
			if err != nil {
				return "", "", Fail("归档文件不存在。", http.StatusNotFound)
			}
			return abs, filepath.Base(abs), nil
		}
		if fileID != "" {
			f, err := manager.File(task, fileID)
			if err != nil {
				return "", "", err
			}
			return f.Path, f.Name, nil
		}
		return "", "", Fail("未指定文件", http.StatusBadRequest)
	}

	mux.HandleFunc("GET /api/tasks/{id}/markdown", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		fileID := r.URL.Query().Get("fileId")
		isArchive := r.URL.Query().Get("archive") == "1"
		target, name, err := resolveTaskMarkdownFile(task, fileID, isArchive)
		if err != nil {
			writeErr(w, err)
			return
		}
		b, err := os.ReadFile(target)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"path":    target,
			"name":    name,
			"content": string(b),
		})
	})

	mux.HandleFunc("PUT /api/tasks/{id}/markdown", func(w http.ResponseWriter, r *http.Request) {
		manager.Lock()
		task, err := manager.Store.Task(r.PathValue("id"))
		manager.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		var in struct {
			FileID  string `json:"fileId"`
			Archive bool   `json:"archive"`
			Content string `json:"content"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		target, name, err := resolveTaskMarkdownFile(task, in.FileID, in.Archive)
		if err != nil {
			writeErr(w, err)
			return
		}
		tmp := target + ".loom.tmp"
		if err := os.WriteFile(tmp, []byte(in.Content), 0o600); err != nil {
			writeErr(w, err)
			return
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			writeErr(w, err)
			return
		}
		manager.Lock()
		if in.Archive {
			_ = os.WriteFile(filepath.Join(manager.Store.Root, "archive", task.ID+".md"), []byte(in.Content), 0o600)
		} else {
			for i := range task.Input {
				if task.Input[i].ID == in.FileID {
					task.Input[i].Size = int64(len(in.Content))
				}
			}
			for i := range task.Output {
				if task.Output[i].ID == in.FileID {
					task.Output[i].Size = int64(len(in.Content))
				}
			}
		}
		manager.Store.Touch(task)
		manager.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"saved": true,
			"path":  target,
			"name":  name,
		})
	})

	// Native macOS folder chooser endpoint
	mux.HandleFunc("POST /api/choose-folder", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		_ = decodeBody(r, &in)
		if in.Title == "" {
			in.Title = "选择文件夹"
		}
		if desktop == nil {
			writeErr(w, Fail("当前模式不支持原生目录选择", http.StatusNotImplemented))
			return
		}
		dir, err := desktop.ChooseFolder(in.Title)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": dir})
	})

	mux.HandleFunc("POST /api/open-folder", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Path string `json:"path"`
		}
		if err := decodeBody(r, &in); err != nil || strings.TrimSpace(in.Path) == "" {
			writeErr(w, Fail("目录路径无效", http.StatusBadRequest))
			return
		}
		expanded := Expand(strings.TrimSpace(in.Path))
		if _, err := os.Stat(expanded); err != nil {
			writeErr(w, Fail("目录不存在："+expanded, http.StatusNotFound))
			return
		}
		if desktop != nil {
			if err := desktop.OpenFolder(expanded); err == nil {
				writeJSON(w, http.StatusOK, map[string]bool{"opened": true})
				return
			}
		}
		if err := exec.Command("open", expanded).Run(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"opened": true})
	})

	mux.HandleFunc("POST /api/open-url", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		if err := decodeBody(r, &in); err != nil || in.URL == "" {
			writeErr(w, Fail("URL 无效", http.StatusBadRequest))
			return
		}
		if desktop != nil {
			if err := desktop.OpenURL(in.URL); err == nil {
				writeJSON(w, http.StatusOK, map[string]bool{"opened": true})
				return
			}
		}
		if err := exec.Command("open", in.URL).Run(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"opened": true})
	})

	mux.HandleFunc("POST /api/copy", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string `json:"text"`
		}
		if err := decodeBody(r, &in); err != nil {
			writeErr(w, err)
			return
		}
		if desktop != nil && desktop.Copy(in.Text) {
			writeJSON(w, http.StatusOK, map[string]bool{"copied": true})
			return
		}
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(in.Text)
		if err := cmd.Run(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"copied": true})
	})

	mux.HandleFunc("POST /api/hooks", func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		if err := decodeBody(r, &ev); err != nil {
			writeErr(w, err)
			return
		}
		validTypes := []string{"identity", "running", "awaiting", "session_end", "message", "output", "exit"}
		if ev.ID == "" || ev.At == "" || ev.TaskID == "" || ev.SessionID == "" || ev.RunID == "" || !slices.Contains(validTypes, ev.Type) {
			writeErr(w, Fail("Hook 事件字段不完整", http.StatusBadRequest))
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if err := manager.Authenticate(ev, token); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "接口不存在"})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inWindow, _ := r.Context().Value(ownWindowKey{}).(bool)
		if !inWindow {
			host := r.Host
			if !loopbackHostRe.MatchString(host) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅支持本机访问"})
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" && origin != "http://"+host && origin != "wails://wails.localhost" && origin != "http://wails.localhost" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "不允许跨站请求"})
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.URL.Path != "/api/hooks" {
			if r.Header.Get("X-Agent-Kanban") != "1" && r.Header.Get("X-Loom") != "1" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "缺少本机请求标识"})
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		mux.ServeHTTP(w, r)
	})
}
