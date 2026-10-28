package kanban

type Tool string
type Mode string
type TaskStatus string
type SessionStatus string

const (
	ToolClaude   Tool = "claude"
	ToolCodex    Tool = "codex"
	ToolPi       Tool = "pi"
	ToolGemini   Tool = "gemini"
	ToolOpenCode Tool = "opencode"
	ToolCursor   Tool = "cursor"

	ModeHeadless Mode = "headless"
	ModeTmux     Mode = "tmux"

	StatusTodo    TaskStatus = "todo"
	StatusDoing   TaskStatus = "doing"
	StatusWaiting TaskStatus = "waiting"
	StatusDone    TaskStatus = "done"

	SessionRunning  SessionStatus = "running"
	SessionAwaiting SessionStatus = "awaiting"
	SessionEnded    SessionStatus = "ended"

	DefaultArchivePath = "/tmp"
)

var ValidTools = []Tool{ToolClaude, ToolCodex, ToolPi, ToolGemini, ToolOpenCode, ToolCursor}

type TaskFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Mime string `json:"mime,omitempty"`
}

type Message struct {
	ID   string `json:"id"`
	Role string `json:"role"` // "user" | "assistant"
	Text string `json:"text"`
	At   string `json:"at"`
}

type Session struct {
	ID           string        `json:"id"`
	Kind         string        `json:"kind"` // "work" | "archive"
	Tool         Tool          `json:"tool"`
	Mode         Mode          `json:"mode"`
	Status       SessionStatus `json:"status"`
	NativeID     string        `json:"nativeId,omitempty"`
	RunID        string        `json:"runId"`
	PID          int           `json:"pid,omitempty"`
	TmuxName     string        `json:"tmuxName,omitempty"`
	StartedAt    string        `json:"startedAt"`
	StateSince   string        `json:"stateSince"`
	EndedAt      string        `json:"endedAt,omitempty"`
	ActiveMs     int64         `json:"activeMs"`
	ExitCode     *int          `json:"exitCode,omitempty"`
	Error        string        `json:"error,omitempty"`
	Messages     []Message     `json:"messages"`
	EventOffset  int64         `json:"eventOffset"`
	RecentEvents []string      `json:"recentEvents"`
}

type GitSnapshot struct {
	Root   string            `json:"root"`
	Branch string            `json:"branch"`
	Head   string            `json:"head"`
	Dirty  map[string]string `json:"dirty"`
}

type GitResult struct {
	Status    string `json:"status"` // "committed" | "unchanged" | "unavailable" | "error"
	Automatic bool   `json:"automatic"`
	Hash      string `json:"hash,omitempty"`
	Message   string `json:"message,omitempty"`
	URL       string `json:"url,omitempty"`
	PRURL     string `json:"prUrl,omitempty"`
	Error     string `json:"error,omitempty"`
}

type CompletionState struct {
	Automatic bool   `json:"automatic"`
	Action    string `json:"action"` // "commit" | "pr"
	Message   string `json:"message"`
}

type ArchiveState struct {
	Status string `json:"status"` // "idle" | "running" | "done" | "error"
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

type Task struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Status      TaskStatus       `json:"status"`
	Tool        Tool             `json:"tool"`
	Mode        Mode             `json:"mode"`
	OneShot     bool             `json:"oneShot"`
	ProjectDir  string           `json:"projectDir"`
	ArchivePath string           `json:"archivePath"`
	CreatedAt   string           `json:"createdAt"`
	UpdatedAt   string           `json:"updatedAt"`
	Input       []TaskFile       `json:"input"`
	Output      []TaskFile       `json:"output"`
	Sessions    []*Session       `json:"sessions"`
	Baseline    *GitSnapshot     `json:"baseline,omitempty"`
	Git         *GitResult       `json:"git,omitempty"`
	Completion  *CompletionState `json:"completion,omitempty"`
	Archive     ArchiveState     `json:"archive"`
	Archived    bool             `json:"archived,omitempty"`
	Error       string           `json:"error,omitempty"`
}

type Board struct {
	Tasks          []*Task  `json:"tasks"`
	Revision       int      `json:"revision"`
	LastProjectDir string   `json:"lastProjectDir"`
	ProjectDirs    []string `json:"projectDirs"`
}

type ToolHealth struct {
	Available bool   `json:"available"`
	Version   string `json:"version"`
}

type Health struct {
	Tools      map[string]ToolHealth `json:"tools"`
	DataDir    string                `json:"dataDir"`
	ProjectDir string                `json:"projectDir"`
}

type GitReview struct {
	Available bool     `json:"available"`
	Root      string   `json:"root,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	Head      string   `json:"head,omitempty"`
	Diff      string   `json:"diff"`
	Files     []string `json:"files"`
	Excluded  []string `json:"excluded"`
	Blocked   string   `json:"blocked,omitempty"`
	Token     string   `json:"token"`
}

type Event struct {
	ID        string   `json:"id"`
	At        string   `json:"at"`
	TaskID    string   `json:"taskId"`
	SessionID string   `json:"sessionId"`
	RunID     string   `json:"runId"`
	Type      string   `json:"type"`
	NativeID  string   `json:"nativeId,omitempty"`
	Text      string   `json:"text,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	ExitCode  *int     `json:"exitCode,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type LaunchSpec struct {
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Stdin      string            `json:"stdin"`
	Mode       Mode              `json:"mode"`
	Cwd        string            `json:"cwd"`
	RunID      string            `json:"runId"`
	LogPath    string            `json:"logPath"`
	FinalPath  string            `json:"finalPath"`
	ResultPath string            `json:"resultPath"`
	Env        map[string]string `json:"env"`
}

// Aggregate computes the task status from its work sessions.
func Aggregate(status TaskStatus, oneShot bool, sessions []*Session) TaskStatus {
	if status == StatusDone {
		return StatusDone
	}
	var work []*Session
	for _, s := range sessions {
		if s.Kind == "work" {
			work = append(work, s)
		}
	}
	if len(work) == 0 {
		return StatusTodo
	}
	for _, s := range work {
		if s.Status == SessionRunning {
			return StatusDoing
		}
	}
	successful := true
	for _, s := range work {
		if s.Status != SessionEnded || s.ExitCode == nil || *s.ExitCode != 0 || s.Error != "" {
			successful = false
			break
		}
	}
	if oneShot && successful {
		return StatusDone
	}
	return StatusWaiting
}
