package kanban

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "runner":
			os.Exit(RunRunner(os.Args[2]))
		case "hook":
			os.Exit(RunHook(os.Args[2:]))
		case "output", "status":
			os.Exit(RunCLICommand(os.Args[1], os.Args[2:]))
		case "fixture-agent":
			os.Exit(RunFixtureAgent(os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}

type testEnv struct {
	t       *testing.T
	root    string
	project string
	manager *Manager
	srv     *httptest.Server
}

func setupTest(t *testing.T, withRepo bool) *testEnv {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if withRepo {
		mustGit(t, project, "init", "-q", "-b", "task-test")
		mustGit(t, project, "config", "user.name", "Loom Test")
		mustGit(t, project, "config", "user.email", "loom-test@localhost")
		_ = os.WriteFile(filepath.Join(project, "app.txt"), []byte("baseline\n"), 0o644)
		_ = os.WriteFile(filepath.Join(project, "notes.txt"), []byte("existing\n"), 0o644)
		mustGit(t, project, "add", "--", "app.txt", "notes.txt")
		mustGit(t, project, "commit", "-qm", "test: 初始内容")
	}

	fixtureWrapper := filepath.Join(root, "fixture-agent")
	script := "#!/bin/sh\nexec " + Quote(SelfBin()) + " fixture-agent \"$@\"\n"
	if err := os.WriteFile(fixtureWrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	execs := Executables{
		ToolClaude:   fixtureWrapper,
		ToolCodex:    fixtureWrapper,
		ToolPi:       fixtureWrapper,
		ToolGemini:   fixtureWrapper,
		ToolOpenCode: fixtureWrapper,
		ToolCursor:   fixtureWrapper,
	}

	opts := ManagerOptions{
		AppRoot:     root,
		DataDir:     filepath.Join(root, "data"),
		URL:         "http://127.0.0.1:1",
		Executables: execs,
	}
	mgr, err := NewManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewAPIHandler(mgr, nil))
	mgr.Options.URL = srv.URL

	t.Cleanup(func() {
		mgr.Close()
		for _, task := range mgr.Store.Data.Tasks {
			for _, s := range task.Sessions {
				if s.Status != SessionEnded {
					_ = mgr.Stop(task, s)
				}
			}
		}
		srv.Close()
	})

	return &testEnv{
		t:       t,
		root:    root,
		project: project,
		manager: mgr,
		srv:     srv,
	}
}

func mustGit(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	out, err := Git(cwd, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return out
}

func (e *testEnv) request(method, path string, body any) (int, map[string]any) {
	e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+"/api"+path, r)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Loom", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&data)
	return resp.StatusCode, data
}

func (e *testEnv) createTask(overrides DraftInput) *Task {
	e.t.Helper()
	in := DraftInput{
		Title:       "测试任务 [write]",
		Description: "验证结果。",
		Tool:        ToolCodex,
		Mode:        ModeHeadless,
		OneShot:     false,
		ProjectDir:  e.project,
		ArchivePath: filepath.Join(e.root, "vault"),
	}
	if overrides.Title != "" {
		in.Title = overrides.Title
	}
	if overrides.Description != "" {
		in.Description = overrides.Description
	}
	if overrides.Tool != "" {
		in.Tool = overrides.Tool
	}
	if overrides.Mode != "" {
		in.Mode = overrides.Mode
	}
	if overrides.OneShot {
		in.OneShot = true
	}
	if overrides.ArchivePath != "" {
		in.ArchivePath = overrides.ArchivePath
	}
	task, err := e.manager.Create(in)
	if err != nil {
		e.t.Fatal(err)
	}
	return task
}

func (e *testEnv) waitTask(task *Task, want TaskStatus) {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		_ = e.manager.Tick()
		e.manager.Lock()
		st := task.Status
		e.manager.Unlock()
		if st == want {
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for task status %s (got %s, err=%s)", want, task.Status, task.Error)
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatal("timeout waiting for condition")
}

func TestAggregate(t *testing.T) {
	sess := func(st SessionStatus, code *int) *Session {
		return &Session{
			ID:         uuid.NewString(),
			Kind:       "work",
			Tool:       ToolCodex,
			Mode:       ModeHeadless,
			Status:     st,
			ExitCode:   code,
			RunID:      uuid.NewString(),
			StartedAt:  NowISO(),
			StateSince: NowISO(),
		}
	}
	zero, two := 0, 2
	if got := Aggregate(StatusTodo, true, nil); got != StatusTodo {
		t.Fatalf("got %s", got)
	}
	if got := Aggregate(StatusDoing, false, []*Session{sess(SessionRunning, nil), sess(SessionAwaiting, nil)}); got != StatusDoing {
		t.Fatalf("got %s", got)
	}
	if got := Aggregate(StatusDoing, false, []*Session{sess(SessionEnded, &zero), sess(SessionAwaiting, nil)}); got != StatusWaiting {
		t.Fatalf("got %s", got)
	}
	if got := Aggregate(StatusDoing, true, []*Session{sess(SessionEnded, &zero), sess(SessionEnded, &zero)}); got != StatusDone {
		t.Fatalf("got %s", got)
	}
	if got := Aggregate(StatusDoing, true, []*Session{sess(SessionEnded, &zero), sess(SessionEnded, &two)}); got != StatusWaiting {
		t.Fatalf("got %s", got)
	}
}

func TestValidationAndPersistence(t *testing.T) {
	e := setupTest(t, true)
	if st, _ := e.request("POST", "/tasks", map[string]any{"title": " "}); st != 400 {
		t.Fatalf("expected 400, got %d", st)
	}
	if st, _ := e.request("POST", "/tasks", map[string]any{"title": "不兼容", "oneShot": true, "mode": "tmux"}); st != 400 {
		t.Fatalf("expected 400, got %d", st)
	}
	vault := filepath.Join(e.root, "vault")
	st, created := e.request("POST", "/tasks", map[string]any{"title": "新任务", "archivePath": vault})
	if st != 201 {
		t.Fatalf("expected 201, got %d", st)
	}
	id := created["id"].(string)
	_, patched := e.request("PATCH", "/tasks/"+id, map[string]any{"title": "中文需求修改", "description": "**需求**"})
	if patched["title"] != "中文需求修改" {
		t.Fatalf("unexpected title: %v", patched["title"])
	}
	restored, err := NewManager(e.manager.Options)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := restored.Store.Task(id)
	if err != nil || rt.Description != "**需求**" || rt.ArchivePath != vault {
		t.Fatalf("unexpected restored task: %+v, err=%v", rt, err)
	}
	_, defTask := e.request("POST", "/tasks", map[string]any{"title": "默认归档任务"})
	if defTask["archivePath"] != "/tmp" {
		t.Fatalf("expected /tmp, got %v", defTask["archivePath"])
	}
	defID := defTask["id"].(string)
	if st, _ := e.request("DELETE", "/tasks/"+defID, nil); st != 200 {
		t.Fatalf("expected 200 on delete, got %d", st)
	}
	if st, _ := e.request("DELETE", "/tasks/"+defID, nil); st != 404 {
		t.Fatalf("expected 404 after delete, got %d", st)
	}
	if _, err := os.Stat(filepath.Join(e.manager.Options.DataDir, "tasks", defID)); !os.IsNotExist(err) {
		t.Fatal("deleted task dir should not exist")
	}
	if _, err := os.Stat(filepath.Join(e.manager.Options.DataDir, "db.json.tmp")); !os.IsNotExist(err) {
		t.Fatal("db.json.tmp should not exist")
	}
}

func TestUploadAndOutputSecurity(t *testing.T) {
	e := setupTest(t, false)
	task := e.createTask(DraftInput{Title: "图片任务"})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("files", "需求图片.png")
	_, _ = fw.Write([]byte("image-content"))
	_ = mw.Close()

	req, _ := http.NewRequest("POST", e.srv.URL+"/api/tasks/"+task.ID+"/files", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Loom", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 201 {
		t.Fatalf("upload failed: status=%v err=%v", resp.StatusCode, err)
	}
	var uploaded []TaskFile
	_ = json.NewDecoder(resp.Body).Decode(&uploaded)
	_ = resp.Body.Close()

	task.Description = "![需求](/api/tasks/" + task.ID + "/files/" + uploaded[0].ID + ")"
	if _, err := e.manager.Launch(task, "", "", ""); err != nil {
		t.Fatal(err)
	}
	promptBytes, _ := os.ReadFile(filepath.Join(e.manager.Store.Root, "tasks", task.ID, "prompt.md"))
	if !strings.Contains(string(promptBytes), uploaded[0].Path) {
		t.Fatal("expected uploaded file path in prompt.md")
	}

	outside := filepath.Join(e.root, "secret.txt")
	_ = os.WriteFile(outside, []byte("private"), 0o600)
	_ = os.Symlink(outside, filepath.Join(e.project, "link.txt"))

	if st, _ := e.request("POST", "/tasks/"+task.ID+"/outputs", map[string]any{"paths": []string{outside}}); st != 400 {
		t.Fatalf("expected 400 for outside file, got %d", st)
	}
	if st, _ := e.request("POST", "/tasks/"+task.ID+"/outputs", map[string]any{"paths": []string{filepath.Join(e.project, "link.txt")}}); st != 400 {
		t.Fatalf("expected 400 for symlink outside, got %d", st)
	}
	e.waitTask(task, StatusWaiting)
}

func TestMultiAgentResumeAndManualArchive(t *testing.T) {
	e := setupTest(t, true)
	task := e.createTask(DraftInput{Title: "并行任务 [slow] [write]"})
	first, err := e.manager.Launch(task, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.manager.Launch(task, ToolPi, "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusWaiting)
	if len(task.Output) != 1 {
		t.Fatalf("expected 1 output, got %d", len(task.Output))
	}
	if first.NativeID != first.ID {
		t.Fatalf("expected nativeId %s, got %s", first.ID, first.NativeID)
	}
	if _, err := e.manager.Chat(task, first, "补充验证", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusWaiting)
	info, err := e.manager.GitReview(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Finish(task, false, "commit", "feat: task 并行任务\n\n- 完成结果验证。", info.Token); err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusDone || task.Git == nil || task.Git.Automatic || task.Git.Status != "committed" {
		t.Fatalf("unexpected git state: %+v", task.Git)
	}
	eventually(t, func() bool {
		_ = e.manager.Tick()
		e.manager.Lock()
		defer e.manager.Unlock()
		return task.Archive.Status == "done"
	})
	noteBytes, err := os.ReadFile(task.Archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	note := string(noteBytes)
	if !strings.Contains(note, "commit_mode: manual") || !strings.Contains(note, "# 已完成的报告") || !strings.Contains(note, task.Git.Hash) {
		t.Fatalf("unexpected archive note:\n%s", note)
	}
}

func TestOneShotAutoCommitAndFailedWait(t *testing.T) {
	e := setupTest(t, true)
	task := e.createTask(DraftInput{Title: "单次任务 [write] [denial]", OneShot: true})
	t.Cleanup(func() {
		if task.Archive.Path != "" {
			_ = os.Remove(task.Archive.Path)
		}
	})
	if _, err := e.manager.Launch(task, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusDone)
	if task.Git == nil || !task.Git.Automatic || task.Git.Status != "committed" {
		t.Fatalf("expected automatic commit, got %+v", task.Git)
	}
	eventually(t, func() bool {
		_ = e.manager.Tick()
		e.manager.Lock()
		defer e.manager.Unlock()
		return task.Archive.Status == "done"
	})

	failed := e.createTask(DraftInput{Title: "失败任务 [fail]", OneShot: true})
	if _, err := e.manager.Launch(failed, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(failed, StatusWaiting)
	if failed.Git != nil || failed.Archive.Status != "idle" {
		t.Fatalf("failed task should not commit or archive")
	}
}

func TestUnrelatedStagedChangesPreserved(t *testing.T) {
	e := setupTest(t, true)
	_ = os.WriteFile(filepath.Join(e.project, "notes.txt"), []byte("unrelated edits\n"), 0o644)
	mustGit(t, e.project, "add", "notes.txt")
	before := mustGit(t, e.project, "diff", "--cached")

	task := e.createTask(DraftInput{OneShot: true})
	if _, err := e.manager.Launch(task, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusDone)
	if got := mustGit(t, e.project, "show", "--format=", "--name-only", "HEAD"); got != "result.md" {
		t.Fatalf("expected only result.md in commit, got %s", got)
	}
	if got := mustGit(t, e.project, "diff", "--cached"); got != before {
		t.Fatalf("staged diff changed: %s", got)
	}
}

func TestHookAuthAndCrossSiteProtection(t *testing.T) {
	e := setupTest(t, true)
	task := e.createTask(DraftInput{Title: "安全测试 [slow]"})
	session, err := e.manager.Launch(task, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{
		ID:        uuid.NewString(),
		At:        NowISO(),
		TaskID:    task.ID,
		SessionID: session.ID,
		RunID:     session.RunID,
		Type:      "awaiting",
	}
	b, _ := json.Marshal(ev)
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/hooks", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	crossReq, _ := http.NewRequest("POST", e.srv.URL+"/api/tasks", strings.NewReader(`{"title":"不应创建"}`))
	crossReq.Header.Set("Content-Type", "application/json")
	crossReq.Header.Set("X-Loom", "1")
	crossReq.Header.Set("Origin", "https://outside.example")
	crossResp, _ := http.DefaultClient.Do(crossReq)
	if crossResp.StatusCode != 403 {
		t.Fatalf("expected 403 for cross-origin, got %d", crossResp.StatusCode)
	}

	unmarkedReq, _ := http.NewRequest("POST", e.srv.URL+"/api/tasks", strings.NewReader(`{"title":"不应创建"}`))
	unmarkedReq.Header.Set("Content-Type", "application/json")
	unmarkedResp, _ := http.DefaultClient.Do(unmarkedReq)
	if unmarkedResp.StatusCode != 403 {
		t.Fatalf("expected 403 for unmarked request, got %d", unmarkedResp.StatusCode)
	}
}

func TestAdaptersUseExplicitSessionID(t *testing.T) {
	e := setupTest(t, false)
	task := e.createTask(DraftInput{Title: "适配器"})
	dir := filepath.Join(e.root, "adapter")
	_ = os.MkdirAll(dir, 0o755)
	taskDir := filepath.Join(e.manager.Store.Root, "tasks", task.ID)
	for _, tool := range ValidTools {
		s := &Session{
			ID:       uuid.NewString(),
			NativeID: "native-test-id",
			Kind:     "work",
			RunID:    uuid.NewString(),
			Tool:     tool,
			Mode:     ModeHeadless,
			Status:   SessionEnded,
		}
		cmd := CommandFor(LaunchOptions{
			Session: s,
			Prompt:  "测试",
			TaskDir: taskDir,
			RunDir:  dir,
			AppRoot: e.root,
		})
		hasID := false
		for _, a := range cmd.Args {
			if a == "native-test-id" {
				hasID = true
			}
			if a == "--last" || a == "--dangerously-bypass-approvals-and-sandbox" || a == "--dangerously-bypass-hook-trust" {
				t.Fatalf("forbidden flag %s in %v", a, cmd.Args)
			}
		}
		if !hasID {
			t.Fatalf("expected native-test-id in args for %s: %v", tool, cmd.Args)
		}
	}
}

func TestPRPushAndCreation(t *testing.T) {
	e := setupTest(t, true)
	remote := filepath.Join(e.root, "remote.git")
	_ = os.MkdirAll(remote, 0o755)
	mustGit(t, remote, "init", "--bare", "-q", "--initial-branch=main")
	mustGit(t, e.project, "remote", "add", "origin", remote)
	mustGit(t, e.project, "branch", "main")
	mustGit(t, e.project, "push", "-q", "origin", "main")

	fakeBin := filepath.Join(e.root, "bin")
	_ = os.MkdirAll(fakeBin, 0o755)
	marker := filepath.Join(e.root, "created-pr.json")
	fakeGh := filepath.Join(fakeBin, "gh")
	ghScript := "#!/bin/sh\n" +
		"if [ \"$1\" = \"repo\" ]; then echo main; exit 0; fi\n" +
		"if [ \"$2\" = \"view\" ]; then\n" +
		"  if [ -f " + Quote(marker) + " ]; then echo https://example.test/pull/1; exit 0; else exit 1; fi\n" +
		"fi\n" +
		"if [ \"$2\" = \"create\" ]; then\n" +
		"  printf '%s' \"$*\" > " + Quote(marker) + "\n" +
		"  echo https://example.test/pull/1; exit 0\n" +
		"fi\nexit 1\n"
	_ = os.WriteFile(fakeGh, []byte(ghScript), 0o755)
	origPath := os.Getenv("PATH")
	_ = os.Setenv("PATH", fakeBin+":"+origPath)
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })

	task := e.createTask(DraftInput{Title: "PR 任务 [write]"})
	if _, err := e.manager.Launch(task, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusWaiting)
	info, err := e.manager.GitReview(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.manager.Finish(task, false, "pr", "feat: task 创建 PR\n\n- 核验任务产物。", info.Token); err != nil {
		t.Fatal(err)
	}
	if task.Git == nil || task.Git.PRURL != "https://example.test/pull/1" {
		t.Fatalf("unexpected PR URL: %+v", task.Git)
	}
	if got := mustGit(t, remote, "rev-parse", "refs/heads/task-test"); got != task.Git.Hash {
		t.Fatalf("remote hash mismatch: %s vs %s", got, task.Git.Hash)
	}
}

func TestOverlappingChangesAndStaleDiff(t *testing.T) {
	e := setupTest(t, true)
	_ = os.WriteFile(filepath.Join(e.project, "result.md"), []byte("pre-existing work\n"), 0o644)
	task1 := e.createTask(DraftInput{Title: "并发任务 A [write]", OneShot: true})
	task2 := e.createTask(DraftInput{Title: "并发任务 B [write]", OneShot: true})
	if _, err := e.manager.Launch(task1, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.manager.Launch(task2, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task1, StatusDone)
	e.waitTask(task2, StatusDone)
	if task1.Git == nil || task1.Git.Status != "committed" {
		t.Fatalf("expected task1 to detect agent commit, got %+v", task1.Git)
	}

	manual := e.createTask(DraftInput{Title: "手动提交检测 [write] [no-commit]", OneShot: false})
	if _, err := e.manager.Launch(manual, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(manual, StatusWaiting)
	info, err := e.manager.GitReview(manual)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(e.project, "app.txt"), []byte("late edits\n"), 0o644)
	st, res := e.request("POST", "/tasks/"+manual.ID+"/complete", map[string]any{
		"token":   info.Token,
		"action":  "commit",
		"message": "feat: task 确认",
	})
	if st != 409 || !strings.Contains(res["error"].(string), "diff 已更新") {
		t.Fatalf("expected 409 stale diff, got %d %v", st, res)
	}
}

func TestOfflineRunnerRecovery(t *testing.T) {
	e := setupTest(t, false)
	e.manager.Options.URL = "http://127.0.0.1:1"
	task := e.createTask(DraftInput{Title: "重启恢复 [slow] [write]"})
	session, err := e.manager.Launch(task, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	e.manager.Close()

	restored, err := NewManager(e.manager.Options)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	recovered, err := restored.Store.Task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		_ = restored.Tick()
		restored.Lock()
		defer restored.Unlock()
		return recovered.Status == StatusWaiting
	})
	if recovered.Sessions[0].ExitCode == nil || *recovered.Sessions[0].ExitCode != 0 {
		t.Fatalf("unexpected exit code: %+v", recovered.Sessions[0])
	}
	if recovered.Sessions[0].NativeID != session.ID || len(recovered.Output) != 1 {
		t.Fatalf("unexpected recovered state: %+v", recovered)
	}
}

func TestMoveWaitingToDoneWithoutCodeChanges(t *testing.T) {
	e := setupTest(t, true)
	cleanTask := e.createTask(DraftInput{Title: "无代码修改任务"})
	if _, err := e.manager.Launch(cleanTask, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(cleanTask, StatusWaiting)

	st, _ := e.request("POST", "/tasks/"+cleanTask.ID+"/move", map[string]any{"status": "done"})
	if st != 200 {
		t.Fatalf("expected 200 moving clean waiting task to done, got %d", st)
	}
	if cleanTask.Status != StatusDone || cleanTask.Git == nil || cleanTask.Git.Status != "unchanged" {
		t.Fatalf("unexpected task state after move to done: status=%s git=%+v", cleanTask.Status, cleanTask.Git)
	}

	dirtyTask := e.createTask(DraftInput{Title: "有代码修改任务 [write]"})
	if _, err := e.manager.Launch(dirtyTask, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(dirtyTask, StatusWaiting)

	st, _ = e.request("POST", "/tasks/"+dirtyTask.ID+"/move", map[string]any{"status": "done"})
	if st != 200 {
		t.Fatalf("expected 200 for waiting task move to done (agent self-commits), got %d", st)
	}
	if dirtyTask.Status != StatusDone || dirtyTask.Git == nil || dirtyTask.Git.Status != "committed" {
		t.Fatalf("expected dirtyTask to be done with agent commit, got status=%s git=%+v", dirtyTask.Status, dirtyTask.Git)
	}
}

func TestDoneTaskArchiveTagAndDelete(t *testing.T) {
	e := setupTest(t, false)
	task := e.createTask(DraftInput{Title: "归档删除测试", OneShot: true})
	if _, err := e.manager.Launch(task, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(task, StatusDone)

	st, patched := e.request("PATCH", "/tasks/"+task.ID, map[string]any{"archived": true})
	if st != 200 || patched["archived"] != true {
		t.Fatalf("expected archived=true, got status=%d body=%v", st, patched)
	}

	st, unarchived := e.request("PATCH", "/tasks/"+task.ID, map[string]any{"archived": false})
	if st != 200 || unarchived["archived"] == true {
		t.Fatalf("expected archived=false, got status=%d body=%v", st, unarchived)
	}

	st, delRes := e.request("DELETE", "/tasks/"+task.ID, nil)
	if st != 200 || delRes["deleted"] != true {
		t.Fatalf("expected deleted=true, got status=%d body=%v", st, delRes)
	}
	if _, err := e.manager.Store.Task(task.ID); err == nil {
		t.Fatal("expected task to be removed from store")
	}
	if _, err := os.Stat(filepath.Join(e.manager.Store.Root, "tasks", task.ID)); !os.IsNotExist(err) {
		t.Fatal("expected task directory to be deleted")
	}
}

func TestSettingsAPIAndTaskDefaults(t *testing.T) {
	e := setupTest(t, false)
	st, getRes := e.request("GET", "/settings", nil)
	if st != 200 {
		t.Fatalf("expected 200 on GET /settings, got %d", st)
	}
	if getRes["theme"] != "system" || getRes["textSize"] != float64(100) || getRes["dock"] != "always" {
		t.Fatalf("unexpected default settings: %v", getRes)
	}

	customArchive := filepath.Join(e.root, "custom-vault")
	st, postRes := e.request("POST", "/settings", map[string]any{
		"theme":              "dark",
		"textSize":           125,
		"dock":               "window",
		"keepAwake":          true,
		"keepAwakeDisplay":   true,
		"sessionTerminal":    "com.googlecode.iterm2",
		"defaultTool":        "codex",
		"defaultOneShot":     false,
		"defaultMode":        "tmux",
		"defaultArchivePath": customArchive,
	})
	if st != 200 {
		t.Fatalf("expected 200 on POST /settings, got %d: %v", st, postRes)
	}
	if postRes["theme"] != "dark" || postRes["textSize"] != float64(125) || postRes["defaultTool"] != "codex" || postRes["defaultMode"] != "tmux" {
		t.Fatalf("unexpected updated settings: %v", postRes)
	}

	if badSt, _ := e.request("POST", "/settings", map[string]any{"theme": "invalid"}); badSt != 400 {
		t.Fatalf("expected 400 for invalid theme, got %d", badSt)
	}

	st, created := e.request("POST", "/tasks", map[string]any{"title": "继承默认设置的任务"})
	if st != 201 {
		t.Fatalf("expected 201 creating task with settings defaults, got %d", st)
	}
	if created["tool"] != "codex" || created["mode"] != "tmux" || created["oneShot"] != false || created["archivePath"] != customArchive {
		t.Fatalf("task did not inherit settings defaults: %v", created)
	}
}

