import { useEffect, useRef, useState } from 'react';
import { Check, ChevronDown, Copy, ExternalLink, Folder, FolderOpen, RefreshCw, Search, X } from 'lucide-react';
import {
  ALL_TOOLS,
  DEFAULT_ARCHIVE_PATH,
  toolLabels,
  type CLIStatus,
  type Health,
  type Mode,
  type Settings,
  type SettingsView,
  type Tool,
} from '../shared/types';
import { api, applyThemeAndZoom, chooseFolder, copyText, openFolder, post, shortPath } from './api';
import { ToolMark } from './components';

export type SettingsTab = 'general' | 'tasks' | 'agents' | 'about';

const SETTINGS_TABS: { id: SettingsTab; label: string; keywords: string }[] = [
  { id: 'general', label: '常规', keywords: 'general 常规 外观 主题 文字大小 缩放 开机自启 登录 dock 防休眠 休眠 通知 提醒 notification 终端 terminal' },
  { id: 'tasks', label: '任务与目录', keywords: 'tasks 任务 目录 默认工具 运行模式 一次性 obsidian 归档 项目目录' },
  { id: 'agents', label: 'Agent 与环境', keywords: 'agents 环境 工具 claude codex pi gemini opencode cursor tmux git gh 路径 executable' },
  { id: 'about', label: '关于', keywords: 'about 关于 版本 数据目录 本地服务 命令行 cli path shell' },
];

interface SegOption<T extends string | number> {
  value: T;
  label: string;
}

function Segmented<T extends string | number>({
  options,
  value,
  onChange,
  ariaLabel,
  disabled = false,
}: {
  options: SegOption<T>[];
  value: T;
  onChange: (next: T) => void;
  ariaLabel?: string;
  disabled?: boolean;
}) {
  return (
    <div className={`magpie-segs ${disabled ? 'disabled' : ''}`} role="group" aria-label={ariaLabel}>
      {options.map(opt => {
        const active = opt.value === value;
        return (
          <button
            key={String(opt.value)}
            type="button"
            className={`magpie-seg-opt ${active ? 'on' : ''}`}
            aria-pressed={active}
            disabled={disabled}
            onClick={() => {
              if (!active && !disabled) onChange(opt.value);
            }}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

interface Props {
  settingsView?: SettingsView;
  health?: Health;
  projectDirs?: string[];
  activeProjectDir: string;
  onPickProject: (dir: string) => Promise<void>;
  onDeleteProject: (dir: string) => Promise<void>;
  onSettingsUpdated: (view: SettingsView) => void;
  onRefreshHealth: () => Promise<void>;
  notify: (msg: string) => void;
}

export function SettingsPage({
  settingsView,
  health,
  projectDirs = [],
  activeProjectDir,
  onPickProject,
  onDeleteProject,
  onSettingsUpdated,
  onRefreshHealth,
  notify,
}: Props) {
  const [tab, setTab] = useState<SettingsTab>(() => {
    const urlTab = new URLSearchParams(window.location.search).get('tab') as SettingsTab | null;
    if (urlTab && SETTINGS_TABS.some(t => t.id === urlTab)) return urlTab;
    try {
      const saved = localStorage.getItem('loom.settingsTab') as SettingsTab | null;
      if (saved && SETTINGS_TABS.some(t => t.id === saved)) return saved;
    } catch {
      // ignore
    }
    return 'general';
  });
  const [query, setQuery] = useState('');
  const [terminalMenuOpen, setTerminalMenuOpen] = useState(false);
  const [archiveDraft, setArchiveDraft] = useState(settingsView?.defaultArchivePath || DEFAULT_ARCHIVE_PATH);
  const [execDrafts, setExecDrafts] = useState<Partial<Record<Tool, string>>>(settingsView?.executables || {});
  const [editingTool, setEditingTool] = useState<Tool | null>(null);
  const [projectInput, setProjectInput] = useState('');
  const [cliStatus, setCliStatus] = useState<CLIStatus>();
  const [cliBusy, setCliBusy] = useState(false);
  const [refreshingHealth, setRefreshingHealth] = useState(false);
  const [scrolled, setScrolled] = useState(false);

  const terminalMenuRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<HTMLElement>(null);

  useEffect(() => {
    if (settingsView) {
      setArchiveDraft(settingsView.defaultArchivePath || DEFAULT_ARCHIVE_PATH);
      setExecDrafts(settingsView.executables || {});
    }
  }, [settingsView]);

  useEffect(() => {
    void api<CLIStatus>('/cli').then(setCliStatus).catch(() => undefined);
  }, []);

  useEffect(() => {
    if (!terminalMenuOpen) return;
    function close(event: MouseEvent) {
      if (terminalMenuRef.current && !terminalMenuRef.current.contains(event.target as Node)) {
        setTerminalMenuOpen(false);
      }
    }
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [terminalMenuOpen]);

  function selectTab(next: SettingsTab) {
    setTab(next);
    try {
      localStorage.setItem('loom.settingsTab', next);
    } catch {
      // ignore
    }
    const url = new URL(window.location.href);
    url.searchParams.set('view', 'settings');
    url.searchParams.set('tab', next);
    window.history.replaceState(null, '', url.toString());
  }

  async function savePatch(patch: Partial<Settings>) {
    try {
      if (patch.theme !== undefined || patch.textSize !== undefined) {
        applyThemeAndZoom(
          patch.theme ?? settingsView?.theme ?? 'system',
          patch.textSize ?? settingsView?.textSize ?? 100,
        );
      }
      const next = await post<SettingsView>('/settings', patch);
      onSettingsUpdated(next);
    } catch (error) {
      notify((error as Error).message);
    }
  }

  async function toggleLogin(on: boolean) {
    try {
      const next = await post<SettingsView>('/settings/login', { on });
      onSettingsUpdated(next);
    } catch (error) {
      notify((error as Error).message);
    }
  }

  async function installCli(shell = '') {
    setCliBusy(true);
    try {
      const res = await post<CLIStatus>('/cli', { shell });
      setCliStatus(res);
      notify('已配置命令行工具，打开新终端窗口即可使用 loom 命令');
    } catch (error) {
      notify((error as Error).message);
    } finally {
      setCliBusy(false);
    }
  }

  const trimmedQuery = query.trim().toLowerCase();
  const isSearching = trimmedQuery.length > 0;

  function matchesRow(partId: SettingsTab, text: string): boolean {
    if (!isSearching) return tab === partId;
    const partMeta = SETTINGS_TABS.find(t => t.id === partId);
    const haystack = `${partMeta?.label || ''} ${partMeta?.keywords || ''} ${text}`.toLowerCase();
    return trimmedQuery.split(/\s+/).every(word => haystack.includes(word));
  }

  const theme = settingsView?.theme || 'system';
  const textSize = settingsView?.textSize || 100;
  const dock = settingsView?.dock || 'always';
  const awakeVal = !settingsView?.keepAwake ? 'off' : settingsView?.keepAwakeDisplay ? 'display' : 'on';
  const notifyTaskEnd = settingsView?.notifyTaskEnd ?? true;
  const notifyProblem =
    settingsView?.notifyProblem === 'denied'
      ? '系统设置里关闭了 Loom 的通知'
      : settingsView?.notifyProblem === 'unavailable'
        ? '此系统无法显示通知'
        : '';
  const defaultTool = settingsView?.defaultTool || 'claude';
  const defaultOneShot = settingsView?.defaultOneShot ?? true;
  const defaultMode: Mode = defaultOneShot ? 'headless' : (settingsView?.defaultMode || 'headless');

  const terminalApps = settingsView?.terminalApps?.length
    ? settingsView.terminalApps
    : [{ id: 'com.apple.Terminal', name: 'Terminal' }];
  const defaultTermApp =
    terminalApps.find(a => a.id === settingsView?.terminalDefault) ||
    terminalApps.find(a => a.id === 'com.apple.Terminal') ||
    terminalApps[0];
  const chosenTerminal = settingsView?.sessionTerminal || 'system';
  const terminalOptions = [
    { id: 'system', name: `系统默认 (${defaultTermApp.name})` },
    ...terminalApps
      .filter(a => a.id !== defaultTermApp.id || a.id === chosenTerminal)
      .map(a => ({ id: a.id, name: a.id === defaultTermApp.id ? `${a.name} (固定)` : a.name })),
  ];
  if (!terminalOptions.some(o => o.id === chosenTerminal)) {
    terminalOptions.push({ id: chosenTerminal, name: `不可用应用 (${chosenTerminal})` });
  }
  const activeTerminalLabel = terminalOptions.find(o => o.id === chosenTerminal)?.name || `系统默认 (${defaultTermApp.name})`;

  // Row visibility per section when searching
  const showAppearance = matchesRow('general', '外观 Appearance 跟随系统 浅色 深色 system light dark');
  const showTextSize = matchesRow('general', '文字大小 Text size 100% 110% 125% 150% 缩放 字体大小');
  const showLogin = matchesRow('general', '开机自启 Open at login 登录项 LaunchAgents 自动启动');
  const showDock = matchesRow('general', '在 Dock 中显示 Show in Dock 隐藏 仅打开窗口时 显示');
  const showKeepAwake = matchesRow('general', '防止休眠 Prevent sleep 保持唤醒 屏幕常亮 caffeinate');
  const showNotifyTaskEnd = matchesRow('general', `任务结束通知 Task completion notification 系统通知 提醒 ${notifyProblem}`);
  const showSessionTerminal = matchesRow('general', `会话终端 Session terminal tmux Terminal iTerm Ghostty Warp kitty ${activeTerminalLabel}`);
  const generalHasHits = showAppearance || showTextSize || showLogin || showDock || showKeepAwake || showNotifyTaskEnd || showSessionTerminal;

  const showDefaultTool = matchesRow('tasks', '默认 Agent 工具 Default agent tool Claude Code Codex Pi Gemini OpenCode Cursor');
  const showDefaultOneShot = matchesRow('tasks', '默认任务类型 一次性任务 持续交流 自动提交并归档 oneShot');
  const showDefaultMode = matchesRow('tasks', '默认运行模式 Default mode headless 后台 tmux 交互终端');
  const showDefaultArchive = matchesRow('tasks', `默认 Obsidian 归档目录 Default archive path ${archiveDraft}`);
  const showProjectDirs = matchesRow('tasks', `常用项目目录 Project directories 工作目录 ${projectDirs.join(' ')}`);
  const tasksHasHits = showDefaultTool || showDefaultOneShot || showDefaultMode || showDefaultArchive || showProjectDirs;

  const showAgentsList = matchesRow('agents', 'Agent 工具与运行环境 claude codex pi gemini opencode cursor 可执行文件路径 版本 状态');
  const showBaseEnv = matchesRow('agents', '基础依赖工具 tmux git gh 版本 状态');
  const agentsHasHits = showAgentsList || showBaseEnv;

  const showVersion = matchesRow('about', `版本 Version ${settingsView?.version || 'dev'}`);
  const showDataDir = matchesRow('about', `数据目录 Config Data folder ~/.loom db.json ${settingsView?.dataDir || ''}`);
  const showCli = matchesRow('about', `命令行工具 Command line loom PATH zsh bash fish ${cliStatus?.command || ''}`);
  const showLocalUrl = matchesRow('about', `本地服务地址 Local server URL hook ${settingsView?.url || ''}`);
  const aboutHasHits = showVersion || showDataDir || showCli || showLocalUrl;

  const anyHits = generalHasHits || tasksHasHits || agentsHasHits || aboutHasHits;

  return (
    <main
      ref={viewRef}
      className={`settings-view ${isSearching ? 'set-searching' : ''}`}
      aria-label="设置"
      onScroll={event => setScrolled(event.currentTarget.scrollTop > 0)}
    >
      <div className={`set-head ${scrolled ? 'stuck' : ''}`}>
        <div
          className="warm-tabs set-tabs"
          id="setTabs"
          role="tablist"
          aria-label="设置"
          onKeyDown={event => {
            const idx = SETTINGS_TABS.findIndex(t => t.id === tab);
            if (idx < 0) return;
            const nextIdx = {
              ArrowLeft: idx - 1,
              ArrowRight: idx + 1,
              Home: 0,
              End: SETTINGS_TABS.length - 1,
            }[event.key];
            if (nextIdx === undefined) return;
            event.preventDefault();
            const target = SETTINGS_TABS[(nextIdx + SETTINGS_TABS.length) % SETTINGS_TABS.length];
            selectTab(target.id);
            document.getElementById(`setTab-${target.id}`)?.focus({ preventScroll: true });
          }}
        >
          {SETTINGS_TABS.map(item => {
            const on = tab === item.id;
            return (
              <button
                key={item.id}
                type="button"
                role="tab"
                id={`setTab-${item.id}`}
                data-set={item.id}
                aria-selected={on}
                aria-controls={`setPage-${item.id}`}
                tabIndex={on ? 0 : -1}
                className={on ? 'on' : ''}
                onClick={() => selectTab(item.id)}
              >
                {item.label}
              </button>
            );
          })}
        </div>
        <label className="search-field set-q">
          <Search size={13} />
          <input
            id="setQ"
            type="search"
            spellCheck={false}
            autoComplete="off"
            placeholder="查找设置"
            aria-label="查找设置"
            value={query}
            onChange={event => setQuery(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Escape' && query) {
                event.preventDefault();
                event.stopPropagation();
                setQuery('');
              }
            }}
          />
          {query && (
            <button
              type="button"
              className="icon-button"
              aria-label="清空设置搜索"
              onClick={() => setQuery('')}
            >
              <X size={12} />
            </button>
          )}
        </label>
      </div>

      {isSearching && !anyHits && (
        <p className="set-none" role="status">
          没有匹配「{query.trim()}」的设置
        </p>
      )}

      {/* 常规 (General) */}
      <section
        className="set-page"
        id="setPage-general"
        role="tabpanel"
        aria-labelledby="setTab-general"
        data-part="常规"
        hidden={isSearching ? !generalHasHits : tab !== 'general'}
      >
        <div className="magpie-list prefs">
          {showAppearance && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">外观</div>
              </div>
              <Segmented
                ariaLabel="外观"
                value={theme}
                options={[
                  { value: 'system', label: '跟随系统' },
                  { value: 'light', label: '浅色' },
                  { value: 'dark', label: '深色' },
                ]}
                onChange={val => void savePatch({ theme: val })}
              />
            </div>
          )}

          {showTextSize && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">文字大小</div>
              </div>
              <Segmented
                ariaLabel="文字大小"
                value={textSize}
                options={[
                  { value: 100, label: '100%' },
                  { value: 110, label: '110%' },
                  { value: 125, label: '125%' },
                  { value: 150, label: '150%' },
                ]}
                onChange={val => void savePatch({ textSize: val })}
              />
            </div>
          )}

          {showLogin && (
            <div className="magpie-row pref" title="登录时在菜单栏启动 Loom，不弹出主窗口">
              <div className="who">
                <div className="name">开机自启</div>
              </div>
              <Segmented
                ariaLabel="开机自启"
                value={settingsView?.login ? 'on' : 'off'}
                options={[
                  { value: 'off', label: '关' },
                  { value: 'on', label: '开' },
                ]}
                onChange={val => void toggleLogin(val === 'on')}
              />
            </div>
          )}

          {showDock && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">在 Dock 中显示</div>
              </div>
              <Segmented
                ariaLabel="在 Dock 中显示"
                value={dock}
                options={[
                  { value: 'never', label: '隐藏' },
                  { value: 'window', label: '仅打开窗口时' },
                  { value: 'always', label: '显示' },
                ]}
                onChange={val => void savePatch({ dock: val })}
              />
            </div>
          )}

          {showKeepAwake && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">防止休眠</div>
                <div className="sub">
                  {awakeVal === 'display'
                    ? 'Loom 运行任务期间保持电脑唤醒并让屏幕常亮'
                    : 'Loom 运行任务期间保持电脑不进入休眠'}
                </div>
              </div>
              <Segmented
                ariaLabel="防止休眠"
                value={awakeVal}
                options={[
                  { value: 'off', label: '关' },
                  { value: 'on', label: '开' },
                  { value: 'display', label: '屏幕也常亮' },
                ]}
                onChange={val =>
                  void savePatch({
                    keepAwake: val !== 'off',
                    keepAwakeDisplay: val === 'display',
                  })
                }
              />
            </div>
          )}

          {showNotifyTaskEnd && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">任务结束通知</div>
                <div className="sub">
                  当 Agent 完成任务、进入等待确认或执行异常时发送系统通知
                  {notifyProblem && (
                    <>
                      {' · '}
                      <span className="warn">{notifyProblem}</span>
                    </>
                  )}
                </div>
              </div>
              <Segmented
                ariaLabel="任务结束通知"
                value={notifyTaskEnd ? 'on' : 'off'}
                options={[
                  { value: 'off', label: '关' },
                  { value: 'on', label: '开' },
                ]}
                onChange={val => void savePatch({ notifyTaskEnd: val === 'on' })}
              />
            </div>
          )}

          {showSessionTerminal && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">会话终端</div>
                <div className="sub">打开交互式 tmux 会话时使用的终端应用</div>
              </div>
              <div className="proto-pick-wrap" ref={terminalMenuRef}>
                <button
                  type="button"
                  className={`proto-pick ${terminalMenuOpen ? 'open' : ''}`}
                  aria-label="会话终端"
                  aria-haspopup="menu"
                  aria-expanded={terminalMenuOpen}
                  onClick={() => setTerminalMenuOpen(!terminalMenuOpen)}
                >
                  <span>{activeTerminalLabel}</span>
                  <ChevronDown size={12} />
                </button>
                {terminalMenuOpen && (
                  <div className="proto-menu" role="menu" aria-label="会话终端">
                    <div className="pm-head">会话终端</div>
                    {terminalOptions.map(opt => {
                      const on = opt.id === chosenTerminal;
                      return (
                        <button
                          key={opt.id}
                          type="button"
                          role="menuitemradio"
                          aria-checked={on}
                          className={`pm-item ${on ? 'on' : ''}`}
                          onClick={() => {
                            setTerminalMenuOpen(false);
                            if (!on) {
                              void savePatch({ sessionTerminal: opt.id === 'system' ? '' : opt.id });
                            }
                          }}
                        >
                          <span className="pm-tick">{on && <Check size={12} />}</span>
                          <span className="pm-words">
                            <span className="pm-name">{opt.name}</span>
                          </span>
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </section>

      {/* 任务与目录 (Tasks & Directories) */}
      <section
        className="set-page"
        id="setPage-tasks"
        role="tabpanel"
        aria-labelledby="setTab-tasks"
        data-part="任务与目录"
        hidden={isSearching ? !tasksHasHits : tab !== 'tasks'}
      >
        {(showDefaultTool || showDefaultOneShot || showDefaultMode || showDefaultArchive) && (
          <>
            <div className="magpie-row-head">
              <span className="label">新建任务默认值</span>
            </div>
            <div className="magpie-list prefs">
              {showDefaultTool && (
                <div className="magpie-row pref">
                  <div className="who">
                    <div className="name">默认 Agent 工具</div>
                    <div className="sub">新建任务时预先选中的编码 Agent</div>
                  </div>
                  <Segmented
                    ariaLabel="默认 Agent 工具"
                    value={defaultTool}
                    options={ALL_TOOLS.map(t => ({ value: t, label: toolLabels[t] }))}
                    onChange={val => void savePatch({ defaultTool: val })}
                  />
                </div>
              )}

              {showDefaultOneShot && (
                <div className="magpie-row pref">
                  <div className="who">
                    <div className="name">默认执行方式</div>
                    <div className="sub">一次性任务在 Agent 正常退出后自动提交 Git 并归档</div>
                  </div>
                  <Segmented
                    ariaLabel="默认执行方式"
                    value={defaultOneShot ? 'oneshot' : 'chat'}
                    options={[
                      { value: 'oneshot', label: '一次性任务' },
                      { value: 'chat', label: '持续交流' },
                    ]}
                    onChange={val =>
                      void savePatch({
                        defaultOneShot: val === 'oneshot',
                        ...(val === 'oneshot' ? { defaultMode: 'headless' } : {}),
                      })
                    }
                  />
                </div>
              )}

              {showDefaultMode && (
                <div className="magpie-row pref">
                  <div className="who">
                    <div className="name">默认运行模式</div>
                    <div className="sub">
                      {defaultOneShot
                        ? '一次性任务固定使用 headless 后台模式'
                        : '持续交流任务默认启动的会话模式'}
                    </div>
                  </div>
                  <Segmented
                    ariaLabel="默认运行模式"
                    value={defaultMode}
                    disabled={defaultOneShot}
                    options={[
                      { value: 'headless', label: 'headless · 后台' },
                      { value: 'tmux', label: 'tmux · 交互终端' },
                    ]}
                    onChange={val => void savePatch({ defaultMode: val })}
                  />
                </div>
              )}

              {showDefaultArchive && (
                <div className="magpie-row pref">
                  <div className="who">
                    <div className="name">默认 Obsidian 归档目录</div>
                    <div className="sub">任务完成后生成的 Markdown 归档笔记默认存放路径</div>
                  </div>
                  <div className="magpie-val">
                    <input
                      className="magpie-words"
                      placeholder={DEFAULT_ARCHIVE_PATH}
                      value={archiveDraft}
                      onChange={event => setArchiveDraft(event.target.value)}
                      onBlur={() => {
                        const trimmed = archiveDraft.trim() || DEFAULT_ARCHIVE_PATH;
                        setArchiveDraft(trimmed);
                        if (trimmed !== (settingsView?.defaultArchivePath || DEFAULT_ARCHIVE_PATH)) {
                          void savePatch({ defaultArchivePath: trimmed });
                        }
                      }}
                      onKeyDown={event => {
                        if (event.key === 'Enter') {
                          event.currentTarget.blur();
                        }
                      }}
                    />
                    <button
                      type="button"
                      className="magpie-text-btn"
                      onClick={() =>
                        void chooseFolder('选择默认 Obsidian 归档目录')
                          .then(dir => {
                            if (dir) {
                              setArchiveDraft(dir);
                              void savePatch({ defaultArchivePath: dir });
                            }
                          })
                          .catch(err => notify((err as Error).message))
                      }
                    >
                      <FolderOpen size={13} />
                      选择…
                    </button>
                  </div>
                </div>
              )}
            </div>
          </>
        )}

        {showProjectDirs && (
          <>
            <div className="magpie-row-head">
              <span className="label">常用项目目录</span>
              <button
                type="button"
                className="magpie-text-btn"
                onClick={() =>
                  void chooseFolder('选择项目目录')
                    .then(dir => {
                      if (dir) void onPickProject(dir);
                    })
                    .catch(err => notify((err as Error).message))
                }
              >
                <FolderOpen size={12} />
                添加文件夹…
              </button>
            </div>
            <div className="magpie-list prefs">
              <form
                className="magpie-row pref"
                onSubmit={event => {
                  event.preventDefault();
                  if (!projectInput.trim()) return;
                  void onPickProject(projectInput.trim()).then(() => setProjectInput(''));
                }}
              >
                <div className="who">
                  <div className="name">添加项目路径</div>
                  <div className="sub">输入本地仓库或工作区目录，新建任务将默认在此目录执行</div>
                </div>
                <div className="magpie-val">
                  <input
                    className="magpie-words"
                    placeholder="如 ~/code/my-project"
                    value={projectInput}
                    onChange={event => setProjectInput(event.target.value)}
                  />
                  <button type="submit" className="magpie-text-btn primary" disabled={!projectInput.trim()}>
                    添加
                  </button>
                </div>
              </form>

              {projectDirs.length > 0 ? (
                projectDirs.map(dir => {
                  const isCurrent = dir === activeProjectDir;
                  return (
                    <div key={dir} className="magpie-row pref">
                      <div className="who">
                        <div className="name project-row-name">
                          <Folder size={13} />
                          <span>{shortPath(dir)}</span>
                          {isCurrent && <span className="magpie-pill-badge on">当前默认</span>}
                        </div>
                        <div className="sub">
                          <code>{dir}</code>
                        </div>
                      </div>
                      <div className="magpie-val">
                        {!isCurrent && (
                          <button
                            type="button"
                            className="magpie-text-btn"
                            onClick={() => void onPickProject(dir)}
                          >
                            设为当前
                          </button>
                        )}
                        <button
                          type="button"
                          className="magpie-text-btn"
                          onClick={() => void openFolder(dir).catch(err => notify((err as Error).message))}
                        >
                          <ExternalLink size={12} />
                          打开
                        </button>
                        <button
                          type="button"
                          className="magpie-text-btn danger"
                          onClick={() => void onDeleteProject(dir)}
                        >
                          移除
                        </button>
                      </div>
                    </div>
                  );
                })
              ) : (
                <div className="magpie-row pref">
                  <div className="who">
                    <div className="sub">暂未添加常用项目目录。</div>
                  </div>
                </div>
              )}
            </div>
          </>
        )}
      </section>

      {/* Agent 与环境 (Agents & Environment) */}
      <section
        className="set-page"
        id="setPage-agents"
        role="tabpanel"
        aria-labelledby="setTab-agents"
        data-part="Agent 与环境"
        hidden={isSearching ? !agentsHasHits : tab !== 'agents'}
      >
        {showAgentsList && (
          <>
            <div className="magpie-row-head">
              <span className="label">Agent 工具</span>
              <button
                type="button"
                className="magpie-text-btn"
                disabled={refreshingHealth}
                onClick={async () => {
                  setRefreshingHealth(true);
                  try {
                    await onRefreshHealth();
                    notify('环境检测已刷新');
                  } finally {
                    setRefreshingHealth(false);
                  }
                }}
              >
                <RefreshCw size={12} className={refreshingHealth ? 'spin' : ''} />
                重新检测
              </button>
            </div>
            <div className="magpie-list prefs">
              {ALL_TOOLS.map(tool => {
                const th = health?.tools?.[tool];
                const customPath = settingsView?.executables?.[tool] || '';
                const isEditing = editingTool === tool;
                const draftVal = execDrafts[tool] ?? customPath;

                return (
                  <div key={tool} className="magpie-row pref">
                    <div className="who">
                      <div className="name agent-row-name">
                        <ToolMark tool={tool} />
                        <span>{toolLabels[tool]}</span>
                        <span className={`magpie-pill-badge ${th?.available ? 'on' : ''}`}>
                          {th ? (th.available ? '可用' : '未检测到') : '检测中'}
                        </span>
                      </div>
                      <div className="sub">
                        {th?.version || '正在读取版本…'}
                        {customPath ? ` · 自定义路径：${customPath}` : ''}
                      </div>
                    </div>
                    <div className="magpie-val">
                      {isEditing ? (
                        <>
                          <input
                            className="magpie-words"
                            placeholder={`默认 (${tool}) 或输入可执行文件绝对路径`}
                            value={draftVal}
                            autoFocus
                            onChange={event =>
                              setExecDrafts(cur => ({ ...cur, [tool]: event.target.value }))
                            }
                            onKeyDown={event => {
                              if (event.key === 'Enter') {
                                const nextExecs = {
                                  ...(settingsView?.executables || {}),
                                  [tool]: draftVal.trim(),
                                };
                                setEditingTool(null);
                                void savePatch({ executables: nextExecs }).then(() => onRefreshHealth());
                              } else if (event.key === 'Escape') {
                                setEditingTool(null);
                                setExecDrafts(settingsView?.executables || {});
                              }
                            }}
                          />
                          <button
                            type="button"
                            className="magpie-text-btn"
                            onClick={() => {
                              setEditingTool(null);
                              setExecDrafts(settingsView?.executables || {});
                            }}
                          >
                            取消
                          </button>
                          <button
                            type="button"
                            className="magpie-text-btn primary"
                            onClick={() => {
                              const nextExecs = {
                                ...(settingsView?.executables || {}),
                                [tool]: draftVal.trim(),
                              };
                              setEditingTool(null);
                              void savePatch({ executables: nextExecs }).then(() => onRefreshHealth());
                            }}
                          >
                            保存
                          </button>
                        </>
                      ) : (
                        <>
                          {customPath && (
                            <button
                              type="button"
                              className="magpie-text-btn"
                              onClick={() => {
                                const nextExecs = { ...(settingsView?.executables || {}) };
                                delete nextExecs[tool];
                                void savePatch({ executables: nextExecs }).then(() => onRefreshHealth());
                              }}
                            >
                              恢复默认
                            </button>
                          )}
                          <button
                            type="button"
                            className="magpie-text-btn"
                            onClick={() => {
                              setExecDrafts(cur => ({ ...cur, [tool]: customPath }));
                              setEditingTool(tool);
                            }}
                          >
                            {customPath ? '修改路径…' : '自定义路径…'}
                          </button>
                        </>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}

        {showBaseEnv && (
          <>
            <div className="magpie-row-head">
              <span className="label">基础运行依赖</span>
            </div>
            <div className="magpie-list prefs">
              {(
                [
                  { key: 'tmux', name: 'tmux', desc: '交互式会话终端复用器' },
                  { key: 'git', name: 'Git', desc: '工作区快照、改动对比与自动提交' },
                  { key: 'gh', name: 'GitHub CLI (gh)', desc: '推送分支并自动创建或关联 Pull Request' },
                ] as const
              ).map(dep => {
                const th = health?.tools?.[dep.key];
                return (
                  <div key={dep.key} className="magpie-row pref">
                    <div className="who">
                      <div className="name agent-row-name">
                        <span>{dep.name}</span>
                        <span className={`magpie-pill-badge ${th?.available ? 'on' : ''}`}>
                          {th ? (th.available ? '已就绪' : '未安装') : '检测中'}
                        </span>
                      </div>
                      <div className="sub">
                        {dep.desc} · {th?.version || '正在检测…'}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </section>

      {/* 关于 (About) */}
      <section
        className="set-page"
        id="setPage-about"
        role="tabpanel"
        aria-labelledby="setTab-about"
        data-part="关于"
        hidden={isSearching ? !aboutHasHits : tab !== 'about'}
      >
        <div className="magpie-list prefs" id="about">
          {showVersion && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">版本</div>
                <div className="sub">macOS 原生多 Agent 任务看板与 Obsidian 归档工作台</div>
              </div>
              <div className="magpie-val">
                <code>{settingsView?.version || 'dev'}</code>
              </div>
            </div>
          )}

          {showDataDir && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">数据目录</div>
                <div className="sub">任务数据库、运行日志、归档备份及当前设置</div>
              </div>
              <div className="magpie-val">
                <code>{settingsView?.dataDir || health?.dataDir || '~/.loom'}</code>
                <button
                  type="button"
                  className="magpie-text-btn"
                  onClick={() =>
                    void copyText(health?.dataDir || settingsView?.dataDir || '~/.loom').then(() =>
                      notify('数据目录路径已复制'),
                    )
                  }
                >
                  <Copy size={12} />
                  复制
                </button>
                <button
                  type="button"
                  className="magpie-text-btn"
                  onClick={() =>
                    void openFolder(health?.dataDir || settingsView?.dataDir || '~/.loom').catch(err =>
                      notify((err as Error).message),
                    )
                  }
                >
                  打开
                </button>
              </div>
            </div>
          )}

          {showCli && (
            <div className="magpie-row pref cli-row">
              <div className="who">
                <div className="name">命令行</div>
                <div className="sub">
                  {!cliStatus
                    ? '正在检查各 Shell 的 PATH…'
                    : cliStatus.ours
                      ? `在新终端中，loom 运行当前应用：${cliStatus.command}`
                      : cliStatus.command
                        ? `在新终端中，loom 指向另一处副本：${cliStatus.command}`
                        : '新终端窗口尚未配置 loom 命令'}
                </div>
                {cliStatus?.shells && cliStatus.shells.length > 0 && (
                  <div className="cli-shells">
                    {cliStatus.shells.map(sh => (
                      <div key={sh.name} className={`cli-shell ${sh.ours ? 'on' : ''}`}>
                        <span className="cli-dot" />
                        <span className="cli-sh">
                          {sh.name}
                          {sh.default ? ' · 默认' : ''}
                        </span>
                        <span className="cli-state">
                          {!sh.known
                            ? '无法读取 PATH'
                            : sh.ours
                              ? '运行当前应用'
                              : sh.command
                                ? `运行 ${sh.command}`
                                : '未找到'}
                        </span>
                        {!sh.ours && sh.profile && (
                          <button
                            type="button"
                            className="magpie-text-btn"
                            disabled={cliBusy}
                            title={`将 ~/.local/bin 加入 ${sh.profile} 并链接 loom`}
                            onClick={() => void installCli(sh.name)}
                          >
                            写入 {sh.profile}
                          </button>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="magpie-val">
                {cliStatus && !cliStatus.ours && (
                  <button
                    type="button"
                    className="magpie-text-btn primary"
                    disabled={cliBusy}
                    onClick={() => void installCli('')}
                  >
                    {cliStatus.command ? '使用当前应用' : '安装到 ~/.local/bin'}
                  </button>
                )}
              </div>
            </div>
          )}

          {showLocalUrl && settingsView?.url && (
            <div className="magpie-row pref">
              <div className="who">
                <div className="name">本地服务地址</div>
                <div className="sub">Agent Hook 与本地 CLI 回报产物所连接的回环地址</div>
              </div>
              <div className="magpie-val">
                <code>{settingsView.url}</code>
                <button
                  type="button"
                  className="magpie-text-btn"
                  onClick={() =>
                    void copyText(settingsView.url).then(() => notify('服务地址已复制'))
                  }
                >
                  <Copy size={12} />
                  复制
                </button>
              </div>
            </div>
          )}
        </div>
      </section>
    </main>
  );
}
