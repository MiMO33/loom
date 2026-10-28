import { useCallback, useEffect, useRef, useState } from 'react';
import { Archive, ArchiveRestore, ArrowUpRight, Check, ChevronDown, ExternalLink, FileText, Folder, FolderOpen, GitBranch, LoaderCircle, MessageSquare, Play, Plus, RefreshCw, Search, Terminal, Trash2, X } from 'lucide-react';
import type { Board, Health, Task, TaskStatus } from '../shared/types';
import { toolLabels } from '../shared/types';
import { api, chooseFolder, copyText, date, openFolder, post, removeProject, removeTask, selectProject, setTaskArchived, shortPath } from './api';
import { SessionRow, ToolMark } from './components';
import { TaskEditor } from './editor';
import { TaskDetail, type DetailTab } from './details';

const columns: { status: TaskStatus; title: string; description: string; empty: string }[] = [
  { status: 'todo', title: '待运行', description: '完善需求，准备出发', empty: '新建任务，或将需求留在这里' },
  { status: 'doing', title: '执行中', description: 'Agent 正在处理', empty: '将待办拖到这里开始运行' },
  { status: 'waiting', title: '待确认', description: '查看结果，继续交流', empty: '需要你确认的任务会出现在这里' },
  { status: 'done', title: '已完成', description: '提交结果，归档过程', empty: '完成的任务会保留在这里' },
];

export default function App() {
  const [board, setBoard] = useState<Board>({ tasks: [], revision: -1 });
  const [health, setHealth] = useState<Health>();
  const [connected, setConnected] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState<string>();
  const [creating, setCreating] = useState(false);
  const [tab, setTab] = useState<DetailTab>('overview');
  const [query, setQuery] = useState('');
  const [now, setNow] = useState(Date.now());
  const [toast, setToast] = useState('');
  const [projectsOpen, setProjectsOpen] = useState(false);
  const [projectInput, setProjectInput] = useState('');
  const [dragging, setDragging] = useState<string>();
  const [over, setOver] = useState<TaskStatus>();
  const [busy, setBusy] = useState<string>();
  const [showArchived, setShowArchived] = useState(false);
  const [contextMenu, setContextMenu] = useState<{ task: Task; x: number; y: number }>();
  const modalRef = useRef<HTMLElement>(null);
  const previousFocus = useRef<HTMLElement | null>(null);
  const projectsWrapRef = useRef<HTMLDivElement>(null);
  const contextMenuRef = useRef<HTMLDivElement>(null);
  const selectedTask = board.tasks.find(task => task.id === selected);
  const overlayOpen = creating || !!selectedTask;
  const activeProjectDir = board.lastProjectDir || board.projectDirs?.[0] || '';

  useEffect(() => {
    if (!overlayOpen) return;
    previousFocus.current = document.activeElement as HTMLElement;
    if (!creating) modalRef.current?.focus();
    return () => previousFocus.current?.focus();
  }, [overlayOpen, creating]);

  useEffect(() => { if (selectedTask && !creating) modalRef.current?.focus(); }, [selected, creating, selectedTask]);

  const refresh = useCallback(async () => {
    try { const data = await api<Board>('/board'); setBoard(previous => previous.revision === data.revision ? previous : data); setConnected(true); setLoaded(true); }
    catch { setConnected(false); setLoaded(true); }
  }, []);

  useEffect(() => { void refresh(); void api<Health>('/health').then(setHealth).catch(() => undefined); const poll = setInterval(() => void refresh(), 1800); const timer = setInterval(() => setNow(Date.now()), 1000); return () => { clearInterval(poll); clearInterval(timer); }; }, [refresh]);
  useEffect(() => { if (!toast) return; const timer = setTimeout(() => setToast(''), 6500); return () => clearTimeout(timer); }, [toast]);
  useEffect(() => {
    function key(event: KeyboardEvent) { if (event.key === 'Escape') { setCreating(false); setSelected(undefined); setProjectsOpen(false); setContextMenu(undefined); } }
    window.addEventListener('keydown', key); return () => window.removeEventListener('keydown', key);
  }, []);

  useEffect(() => {
    if (!contextMenu) return;
    function closeMenu(event: Event) {
      if (event instanceof MouseEvent && contextMenuRef.current?.contains(event.target as Node)) return;
      setContextMenu(undefined);
    }
    document.addEventListener('mousedown', closeMenu);
    window.addEventListener('scroll', closeMenu, true);
    window.addEventListener('resize', closeMenu);
    window.addEventListener('blur', closeMenu);
    return () => {
      document.removeEventListener('mousedown', closeMenu);
      window.removeEventListener('scroll', closeMenu, true);
      window.removeEventListener('resize', closeMenu);
      window.removeEventListener('blur', closeMenu);
    };
  }, [contextMenu]);

  useEffect(() => {
    if (!projectsOpen) return;
    function handleOutside(event: MouseEvent) {
      if (projectsWrapRef.current && !projectsWrapRef.current.contains(event.target as Node)) {
        setProjectsOpen(false);
      }
    }
    document.addEventListener('mousedown', handleOutside);
    return () => document.removeEventListener('mousedown', handleOutside);
  }, [projectsOpen]);

  async function pickProject(dir: string) {
    const trimmed = dir.trim();
    if (!trimmed) return;
    try {
      const res = await selectProject(trimmed);
      setBoard(current => ({ ...current, lastProjectDir: res.lastProjectDir, projectDirs: res.projectDirs }));
      setProjectInput('');
      await refresh();
    } catch (error) {
      setToast((error as Error).message);
    }
  }

  async function deleteProject(dir: string) {
    try {
      const res = await removeProject(dir);
      setBoard(current => ({ ...current, lastProjectDir: res.lastProjectDir, projectDirs: res.projectDirs }));
      await refresh();
    } catch (error) {
      setToast((error as Error).message);
    }
  }

  async function openProjectInFinder(dir: string) {
    try {
      await openFolder(dir);
    } catch (error) {
      setToast((error as Error).message);
    }
  }

  function open(task: Task, target: DetailTab = 'overview') { setCreating(false); setSelected(task.id); setTab(target); }
  function close() { setCreating(false); setSelected(undefined); }
  async function run(task: Task) {
    setBusy(task.id);
    try { await post(`/tasks/${task.id}/run`, { projectDir: task.projectDir || activeProjectDir }); await refresh(); setToast('任务已启动'); }
    catch (error) { setToast((error as Error).message); }
    finally { setBusy(undefined); }
  }
  async function attach(task: Task, sessionId: string) {
    setBusy(task.id);
    try {
      const result = await post<{ command: string; opened: boolean }>(`/tasks/${task.id}/sessions/${sessionId}/attach`);
      if (result.opened) setToast('已在 Terminal 打开 tmux 会话');
      else { await copyText(result.command); setToast('attach 命令已复制，请在终端执行'); }
      await refresh();
    } catch (error) { setToast((error as Error).message); }
    finally { setBusy(undefined); }
  }

  async function move(task: Task, status: TaskStatus) {
    if (task.status === status) return;
    setBusy(task.id);
    try {
      await post(`/tasks/${task.id}/move`, { status });
      await refresh();
      if (status === 'done' && task.status === 'waiting') setToast('任务已完成，正在生成归档');
    } catch (error) {
      const msg = (error as Error).message;
      if (status === 'done' && task.status === 'waiting' && msg.includes('查看 diff')) {
        open(task, 'diff');
      } else {
        setToast(msg);
      }
    } finally {
      setBusy(undefined);
    }
  }

  async function archiveDoneTask(task: Task) {
    setBusy(task.id);
    try {
      if (task.archive.status !== 'done' && task.archive.status !== 'running') {
        await post(`/tasks/${task.id}/archive`);
      }
      await setTaskArchived(task.id, !task.archived);
      await refresh();
      setToast(task.archived ? '已移回已完成列表' : '已归档任务');
    } catch (error) {
      setToast((error as Error).message);
    } finally {
      setBusy(undefined);
    }
  }

  async function deleteTask(task: Task) {
    setContextMenu(undefined);
    if (!window.confirm(`确认删除任务「${task.title}」？`)) return;
    setBusy(task.id);
    try {
      await removeTask(task.id);
      if (selected === task.id) setSelected(undefined);
      await refresh();
      setToast('任务已删除');
    } catch (error) {
      setToast((error as Error).message);
    } finally {
      setBusy(undefined);
    }
  }

  const filtered = board.tasks.filter(task => [task.title, task.description, task.projectDir, ...task.output.map(file => file.name)].join('\n').toLowerCase().includes(query.toLowerCase()));

  return <div className="app-shell">
    <header className="app-header" style={{ '--wails-draggable': 'drag' } as React.CSSProperties}>
      <div className="brand">
        <img className="brand-logo" src="/logo.png" alt="" aria-hidden="true" draggable={false} />
        <h1>Loom</h1>
      </div>
      <div className="header-actions" style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}>
        <label className="search-field"><Search size={13} /><input aria-label="搜索任务" placeholder="搜索任务…" value={query} onChange={event => setQuery(event.target.value)} />{query && <button className="icon-button" aria-label="清空搜索" onClick={() => setQuery('')}><X size={12} /></button>}</label>
        <div className="environment-wrap" ref={projectsWrapRef}>
          <button
            className="environment-button"
            onClick={() => setProjectsOpen(!projectsOpen)}
            aria-expanded={projectsOpen}
            title={activeProjectDir || '管理项目目录'}
          >
            <Folder size={13} />
            <span className="project-trigger-label">{activeProjectDir ? shortPath(activeProjectDir) : '项目目录'}</span>
            <ChevronDown size={11} />
          </button>
          {projectsOpen && (
            <div className="environment-panel project-panel">
              <div className="section-heading first">
                <h3>项目目录</h3>
                <button
                  type="button"
                  className="text-button"
                  onClick={() =>
                    void chooseFolder('选择项目目录')
                      .then(dir => {
                        if (dir) void pickProject(dir);
                      })
                      .catch(error => setToast((error as Error).message))
                  }
                >
                  <FolderOpen size={12} />
                  选择文件夹…
                </button>
              </div>
              <form
                className="project-add-form"
                onSubmit={event => {
                  event.preventDefault();
                  void pickProject(projectInput);
                }}
              >
                <input
                  aria-label="输入项目目录路径"
                  placeholder="输入路径，如 ~/code/my-project"
                  value={projectInput}
                  onChange={event => setProjectInput(event.target.value)}
                />
                <button type="submit" className="button small" disabled={!projectInput.trim()}>
                  添加
                </button>
              </form>
              {board.projectDirs && board.projectDirs.length > 0 ? (
                <div className="project-dir-list" role="list" aria-label="常用项目目录">
                  {board.projectDirs.map(dir => {
                    const active = activeProjectDir === dir;
                    return (
                      <div key={dir} role="listitem" className={`project-dir-row ${active ? 'active' : ''}`}>
                        <button
                          type="button"
                          className="project-dir-main"
                          title={dir}
                          onClick={() => void pickProject(dir)}
                        >
                          <Folder size={13} />
                          <div className="project-dir-text">
                            <strong>{shortPath(dir)}</strong>
                            <small>{dir}</small>
                          </div>
                          {active && <Check size={13} className="project-dir-check" />}
                        </button>
                        <div className="project-dir-actions">
                          <button
                            type="button"
                            className="icon-button"
                            title="在 Finder 中打开"
                            aria-label={`在 Finder 中打开 ${dir}`}
                            onClick={() => void openProjectInFinder(dir)}
                          >
                            <ExternalLink size={12} />
                          </button>
                          <button
                            type="button"
                            className="icon-button"
                            title="移除目录"
                            aria-label={`移除 ${dir}`}
                            onClick={() => void deleteProject(dir)}
                          >
                            <X size={12} />
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <p className="muted project-empty">暂未设置项目目录，请选择或添加本地项目路径。</p>
              )}
              {health?.dataDir && (
                <div className="health-storage">
                  <small>自动记忆上次选择的项目目录；新任务与新 Agent 均在项目目录执行。</small>
                  <code>{health.dataDir}</code>
                </div>
              )}
            </div>
          )}
        </div>
        <button className="button primary small new-task" onClick={() => { setSelected(undefined); setCreating(true); setProjectsOpen(false); }}><Plus size={14} />新建任务</button>
      </div>
    </header>
    {!connected && <div className="connection-banner" role="alert">本地服务暂时无法连接。任务数据保存在本机，服务恢复后会自动刷新。<button className="text-button" onClick={() => void refresh()}>重试连接</button></div>}
    <main className="board" aria-label="任务看板">{columns.map(column => {
      const columnTasks = filtered.filter(task => task.status === column.status);
      const archivedCount = column.status === 'done' ? columnTasks.filter(task => task.archived).length : 0;
      const tasks = column.status === 'done' && !showArchived ? columnTasks.filter(task => !task.archived) : columnTasks;
      return <section key={column.status} className={`board-column ${column.status} ${over === column.status ? 'drop-target' : ''}`} aria-label={column.title} onDragOver={event => { if (dragging) { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; setOver(column.status); } }} onDragLeave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node)) setOver(undefined); }} onDrop={event => { event.preventDefault(); const task = board.tasks.find(task => task.id === dragging); setDragging(undefined); setOver(undefined); if (task) void move(task, column.status); }}>
        <div className="column-heading"><div><i /><h2>{column.status}</h2><span className="column-label">{column.title}</span><span className="column-count">{tasks.length}</span></div>{column.status === 'todo' && <button className="icon-button" aria-label="新建待办任务" onClick={() => { setCreating(true); setSelected(undefined); }}><Plus size={16} /></button>}{column.status === 'done' && (archivedCount > 0 || showArchived) && <button className={`done-filter-badge ${showArchived ? 'active' : ''}`} onClick={() => setShowArchived(!showArchived)}><Archive size={11} />{showArchived ? `隐藏已归档 (${archivedCount})` : `已归档 (${archivedCount})`}</button>}</div><p className="column-description">{column.description}</p>
        <div className="column-cards">{!loaded ? <div className="column-empty"><LoaderCircle size={20} className="spin" /><p>加载任务…</p></div> : tasks.length ? tasks.map(task => {
          const sessions = task.sessions.filter(session => session.kind === 'work');
          const taskProject = task.projectDir || activeProjectDir;
          return <article className={`task-card ${dragging === task.id ? 'dragging' : ''} ${selected === task.id ? 'selected' : ''} ${task.archived ? 'is-archived' : ''}`} key={task.id} draggable={!busy && task.status !== 'done'} tabIndex={0} role="button" aria-label={`查看任务：${task.title}`} onClick={() => open(task)} onContextMenu={event => { event.preventDefault(); event.stopPropagation(); const x = Math.min(event.clientX, window.innerWidth - 180); const y = Math.min(event.clientY, window.innerHeight - 60); setContextMenu({ task, x, y }); }} onKeyDown={event => { if (event.target === event.currentTarget && ['Enter', ' '].includes(event.key)) { event.preventDefault(); open(task); } }} onDragStart={event => { setContextMenu(undefined); setDragging(task.id); event.dataTransfer.setData('text/plain', task.id); event.dataTransfer.effectAllowed = 'move'; }} onDragEnd={() => { setDragging(undefined); setOver(undefined); }}>
            <div className="card-topline"><span className="task-number">#{task.id.slice(0, 6)}</span>{task.status === 'done' && <span className={`card-status-tag ${task.archived ? 'archived' : task.archive.status === 'done' ? 'obsidian' : task.archive.status === 'error' ? 'error' : ''}`}>{task.archived ? '已归档' : task.archive.status === 'done' ? '已存笔记' : task.archive.status === 'running' ? '归档中' : task.archive.status === 'error' ? '归档失败' : '待归档'}</span>}<span className="card-type">{task.oneShot ? '一次性' : '持续交流'}</span><ArrowUpRight size={13} className="card-open" /></div><h3>{task.title}</h3>{task.description && <p className="card-description">{task.description.replace(/!\[.*?\]\(.*?\)/g, '[图片]').replace(/[#*`>]/g, '')}</p>}
            {taskProject && <div className="card-project" title={taskProject}><Folder size={12} /><span>{shortPath(taskProject)}</span></div>}
            <div className="card-sessions">{sessions.length ? sessions.map(session => <SessionRow compact key={session.id} session={session} now={now} onAttach={session.mode === 'tmux' && task.status !== 'done' ? () => void attach(task, session.id) : undefined} />) : <div className="unstarted"><ToolMark tool={task.tool} /><span>{toolLabels[task.tool]}</span><span className="mode-label">{task.mode}</span><span>尚未运行</span></div>}</div>
            {task.error && <p className="card-error" title={task.error}>{task.error}</p>}
            {task.status === 'done' && <div className="card-completion"><div><GitBranch size={12} />{task.git?.status === 'committed' ? <><span>{task.git.automatic ? '自动提交' : '手动提交'}</span><code>{task.git.hash?.slice(0, 7)}</code></> : <span>{task.git?.status === 'unavailable' ? '无 Git 仓库' : task.git?.status === 'error' ? '提交失败' : '无新改动'}</span>}</div>{task.git?.status === 'committed' && <p title={task.git.message}>{task.git.message?.split('\n')[0]}</p>}<div className={task.archive.status === 'error' ? 'archive-error' : 'archive-indicator'}>{task.archive.status === 'running' ? <LoaderCircle size={12} className="spin" /> : task.archive.status === 'done' ? <Check size={12} /> : <Archive size={12} />}{task.archive.status === 'running' ? '正在归档到 Obsidian' : task.archive.status === 'done' ? '已归档到 Obsidian' : task.archive.status === 'error' ? '归档失败 · 点击重试' : '等待归档'}</div></div>}
            <div className="card-footer"><span><FileText size={12} />{task.output.length} 个输出</span><time>{date(task.createdAt)}</time></div>
            {task.status === 'todo' && <div className="card-actions"><button className="text-button" disabled={!!busy} onClick={event => { event.stopPropagation(); void run(task); }}>{busy === task.id ? <LoaderCircle size={12} className="spin" /> : <Play size={12} />}运行任务</button><span>拖至 doing 启动</span></div>}
            {task.status === 'waiting' && <div className="card-actions"><button className="text-button confirm-button" disabled={!!busy} onClick={event => { event.stopPropagation(); void move(task, 'done'); }}>{busy === task.id ? <LoaderCircle size={12} className="spin" /> : <Check size={13} />}确认完成</button><button className="text-button" onClick={event => { event.stopPropagation(); open(task, 'sessions'); }}><MessageSquare size={12} />继续交流</button></div>}
            {task.status === 'doing' && sessions.some(s => s.mode === 'tmux') && <div className="card-actions"><button className="text-button" onClick={event => { event.stopPropagation(); open(task, 'sessions'); }}><Terminal size={12} />打开 session</button></div>}
            {task.status === 'done' && <div className="card-actions done-card-actions"><div className="card-tag-group"><button type="button" className={`card-tag-button ${task.archived ? 'active' : ''}`} disabled={!!busy} title={task.archived ? '取消归档，恢复显示在已完成列表' : '归档任务并从看板收起'} onClick={event => { event.stopPropagation(); void archiveDoneTask(task); }}>{busy === task.id ? <LoaderCircle size={11} className="spin" /> : task.archived ? <ArchiveRestore size={11} /> : <Archive size={11} />}{task.archived ? '取消归档' : '归档'}</button>{task.archive.status === 'error' && <button type="button" className="card-tag-button" disabled={!!busy} title="重试 Obsidian 归档" onClick={event => { event.stopPropagation(); setBusy(task.id); void post(`/tasks/${task.id}/archive`).then(() => refresh()).then(() => setToast('已重新启动归档')).catch(err => setToast((err as Error).message)).finally(() => setBusy(undefined)); }}><RefreshCw size={11} />重试笔记</button>}</div><button type="button" className="card-tag-button danger" disabled={!!busy} title="删除任务" onClick={event => { event.stopPropagation(); void deleteTask(task); }}><Trash2 size={11} />删除</button></div>}
          </article>;
        }) : <div className="column-empty"><span className="empty-symbol">{column.status === 'todo' ? <Plus size={20} /> : column.status === 'doing' ? <Play size={18} /> : column.status === 'waiting' ? <MessageSquare size={19} /> : <Check size={20} />}</span><p>{query ? '没有匹配的任务' : column.status === 'done' && archivedCount > 0 ? `已完成任务均已归档（共 ${archivedCount} 个）` : column.empty}</p>{column.status === 'todo' && !query && <button className="text-button" onClick={() => setCreating(true)}>新建第一个任务 <Plus size={12} /></button>}{column.status === 'done' && !query && archivedCount > 0 && !showArchived && <button className="text-button" onClick={() => setShowArchived(true)}>查看已归档任务 <Archive size={12} /></button>}</div>}</div>
      </section>;
    })}</main>
    {overlayOpen && (
      <div className="new-task-backdrop" onClick={close}>
        <section
          className="new-task-modal"
          ref={modalRef}
          tabIndex={-1}
          role="dialog"
          aria-modal="true"
          aria-label={creating ? '新建任务' : '任务详情'}
          onClick={event => event.stopPropagation()}
          onKeyDown={event => {
            if (event.key !== 'Tab') return;
            const elements = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled):not([hidden]), textarea:not(:disabled), select:not(:disabled), [contenteditable="true"], a[href], summary')).filter(element => element.offsetParent !== null);
            const first = elements[0]; const last = elements.at(-1);
            if (event.shiftKey && (document.activeElement === first || document.activeElement === modalRef.current)) { event.preventDefault(); last?.focus(); }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
          }}
        >
          {creating ? (
            <>
              <header className="new-task-modal-header">
                <div className="new-task-modal-title">
                  <span className="eyebrow">NEW TASK</span>
                  <h2>新建任务</h2>
                </div>
                <button className="icon-button close-button" aria-label="关闭新建任务" onClick={close}><X size={18} /></button>
              </header>
              <div className="new-task-modal-body">
                <TaskEditor
                  wide
                  health={health}
                  defaultProjectDir={activeProjectDir}
                  projectDirs={board.projectDirs}
                  onProjectsChange={() => void refresh()}
                  notify={setToast}
                  onSaved={task => {
                    setBoard(current => ({
                      ...current,
                      tasks: current.tasks.some(item => item.id === task.id)
                        ? current.tasks.map(item => (item.id === task.id ? task : item))
                        : [task, ...current.tasks],
                    }));
                    void refresh();
                    close();
                  }}
                />
              </div>
            </>
          ) : (
            selectedTask && (
              <TaskDetail
                key={selectedTask.id}
                task={selectedTask}
                now={now}
                health={health}
                defaultProjectDir={activeProjectDir}
                projectDirs={board.projectDirs}
                tab={tab}
                setTab={setTab}
                refresh={refresh}
                notify={setToast}
                close={close}
              />
            )
          )}
        </section>
      </div>
    )}
    {contextMenu && (
      <div
        ref={contextMenuRef}
        className="task-context-menu"
        role="menu"
        aria-label={`任务操作：${contextMenu.task.title}`}
        style={{ left: contextMenu.x, top: contextMenu.y }}
      >
        <button
          type="button"
          role="menuitem"
          className="task-context-menu-item danger"
          disabled={!!busy}
          onClick={() => void deleteTask(contextMenu.task)}
        >
          <Trash2 size={13} />
          <span>删除任务</span>
        </button>
      </div>
    )}
    {toast && <div className="toast" role="status"><span>{toast}</span><button className="icon-button" aria-label="关闭提示" onClick={() => setToast('')}><X size={14} /></button></div>}
  </div>;
}
