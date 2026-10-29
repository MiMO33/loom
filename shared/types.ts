export type Tool = 'claude' | 'codex' | 'pi' | 'gemini' | 'opencode' | 'cursor';
export type Mode = 'headless' | 'tmux';
export type TaskStatus = 'todo' | 'doing' | 'waiting' | 'done';
export type SessionStatus = 'running' | 'awaiting' | 'ended';

export const DEFAULT_ARCHIVE_PATH = '/tmp';
export const ALL_TOOLS: Tool[] = ['claude', 'codex', 'pi', 'gemini', 'opencode', 'cursor'];

export interface TaskFile {
  id: string;
  name: string;
  path: string;
  size: number;
  mime?: string;
}

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  at: string;
}

export interface Session {
  id: string;
  kind: 'work' | 'archive';
  tool: Tool;
  mode: Mode;
  status: SessionStatus;
  nativeId?: string;
  runId: string;
  pid?: number;
  tmuxName?: string;
  startedAt: string;
  stateSince: string;
  endedAt?: string;
  activeMs: number;
  exitCode?: number;
  error?: string;
  messages: Message[];
  eventOffset: number;
  recentEvents: string[];
}

export interface GitSnapshot {
  root: string;
  branch: string;
  head: string;
  dirty: Record<string, string>;
}

export interface GitResult {
  status: 'committed' | 'unchanged' | 'unavailable' | 'error';
  automatic: boolean;
  hash?: string;
  message?: string;
  url?: string;
  prUrl?: string;
  error?: string;
}

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  tool: Tool;
  mode: Mode;
  oneShot: boolean;
  projectDir: string;
  archivePath: string;
  createdAt: string;
  updatedAt: string;
  input: TaskFile[];
  output: TaskFile[];
  sessions: Session[];
  baseline?: GitSnapshot;
  git?: GitResult;
  completion?: { automatic: boolean; action: 'commit' | 'pr'; message: string };
  archive: { status: 'idle' | 'running' | 'done' | 'error'; path?: string; error?: string };
  archived?: boolean;
  error?: string;
}

export interface TerminalChoice {
  id: string;
  name: string;
  path?: string;
}

export interface Settings {
  theme?: 'system' | 'light' | 'dark';
  textSize?: 100 | 110 | 125 | 150;
  dock?: 'always' | 'window' | 'never';
  keepAwake?: boolean;
  keepAwakeDisplay?: boolean;
  notifyTaskEnd?: boolean;
  sessionTerminal?: string;
  defaultTool?: Tool;
  defaultMode?: Mode;
  defaultOneShot?: boolean;
  defaultArchivePath?: string;
  executables?: Partial<Record<Tool, string>>;
}

export interface SettingsView extends Settings {
  login: boolean;
  notifyProblem?: 'denied' | 'unavailable';
  version: string;
  dataDir: string;
  url: string;
  terminalApps: TerminalChoice[];
  terminalDefault: string;
}

export interface CLIShell {
  name: string;
  default?: boolean;
  known: boolean;
  command?: string;
  ours: boolean;
  profile?: string;
  hasDir?: boolean;
}

export interface CLIStatus {
  exe: string;
  command?: string;
  ours: boolean;
  dir?: string;
  shells: CLIShell[];
}

export interface Board {
  tasks: Task[];
  revision: number;
  lastProjectDir?: string;
  projectDirs?: string[];
  settings?: Settings;
}

export interface ToolHealth { available: boolean; version: string }
export interface Health {
  tools: Record<Tool | 'tmux' | 'git' | 'gh', ToolHealth>;
  dataDir: string;
  projectDir: string;
}

export interface GitReview {
  available: boolean;
  root?: string;
  branch?: string;
  head?: string;
  diff: string;
  files: string[];
  excluded: string[];
  blocked?: string;
  token: string;
}

export const toolLabels: Record<Tool, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  pi: 'Pi',
  gemini: 'Gemini CLI',
  opencode: 'OpenCode',
  cursor: 'Cursor Agent',
};
export const statusLabels: Record<SessionStatus, string> = { running: '运行中', awaiting: '等待输入', ended: '已结束' };

export function aggregate(task: Pick<Task, 'sessions' | 'oneShot' | 'status'>): TaskStatus {
  if (task.status === 'done') return 'done';

  const sessions = task.sessions.filter(s => s.kind === 'work');
  if (!sessions.length) return 'todo';
  if (sessions.some(s => s.status === 'running')) return 'doing';

  const successful = sessions.every(s => s.status === 'ended' && s.exitCode === 0 && !s.error);
  return task.oneShot && successful ? 'done' : 'waiting';
}
