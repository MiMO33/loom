import { useEffect, useState } from 'react';
import { Archive, ArchiveRestore, ArrowUpRight, Check, Clipboard, Download, FileText, FolderOpen, GitBranch, LoaderCircle, MessageSquare, Pencil, Play, Plus, RefreshCw, Send, Square, Terminal, Trash2, X } from 'lucide-react';
import { ALL_TOOLS, DEFAULT_ARCHIVE_PATH, toolLabels, type GitReview, type Health, type Mode, type Session, type Task, type Tool } from '../shared/types';
import { api, chooseFolder, confirmAction, copyText, date, openExternal, openFolder, post, removeTask, selectProject, setTaskArchived, stripAnsi } from './api';
import { Files, Markdown, SessionRow } from './components';
import { TaskEditor } from './editor';
import { MarkdownDocumentModal, type MarkdownDocumentTarget } from './markdown-editor/OrcaMarkdownEditor';

export type DetailTab = 'overview' | 'sessions' | 'diff' | 'archive';
interface Props {
  task: Task; now: number; health?: Health; defaultProjectDir?: string; projectDirs?: string[];
  tab: DetailTab; setTab: (tab: DetailTab) => void;
  refresh: () => Promise<void>; notify: (message: string) => void; close: () => void;
}

export function TaskDetail({ task, now, health, defaultProjectDir, projectDirs, tab, setTab, refresh, notify, close }: Props) {
  const [editing, setEditing] = useState(false);
  const [activeSession, setActiveSession] = useState(task.sessions.filter(s => s.kind === 'work').at(-1)?.id || '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [adding, setAdding] = useState(false);
  const [tool, setTool] = useState(task.tool);
  const [mode, setMode] = useState(task.mode);
  const [message, setMessage] = useState('');
  const [logs, setLogs] = useState('');
  const [logError, setLogError] = useState('');
  const [review, setReview] = useState<GitReview>();
  const [reviewError, setReviewError] = useState('');
  const [reviewLoading, setReviewLoading] = useState(false);
  const [commitMessage, setCommitMessage] = useState(`feat: task ${task.title}\n\n- 完成任务 ${task.id} 的改动。`);
  const [docModal, setDocModal] = useState<MarkdownDocumentTarget | null>(null);
  const session = task.sessions.find(s => s.id === activeSession) || task.sessions.filter(s => s.kind === 'work').at(-1);
  const effectiveProjectDir = task.projectDir || defaultProjectDir || projectDirs?.[0] || '';
  const workSessions = task.sessions.filter(s => s.kind === 'work');
  const latestAnswer = [...workSessions]
    .reverse()
    .flatMap(s => [...s.messages].reverse())
    .find(m => m.role === 'assistant');

  async function perform(action: () => Promise<unknown>, success?: string) {
    setBusy(true); setError('');
    try { await action(); await refresh(); if (success) notify(success); return true; }
    catch (error) { setError((error as Error).message); return false; }
    finally { setBusy(false); }
  }

  async function loadReview() {
    setReviewLoading(true); setReviewError('');
    try { setReview(await api<GitReview>(`/tasks/${task.id}/review`)); }
    catch (error) { setReview(undefined); setReviewError((error as Error).message); }
    finally { setReviewLoading(false); }
  }

  useEffect(() => { if (tab === 'diff') void loadReview(); }, [tab, task.id, task.git?.hash]);
  useEffect(() => {
    if (tab !== 'sessions' || !session) return;
    let cancelled = false;
    setLogs(''); setLogError('');
    async function load() {
      try {
        const result = await api<{ text: string; truncated: boolean }>(`/tasks/${task.id}/sessions/${session!.id}/log`);
        if (!cancelled) { setLogs((result.truncated ? '（显示最近 160 KB 日志）\n\n' : '') + stripAnsi(result.text)); setLogError(''); }
      } catch (error) { if (!cancelled) setLogError((error as Error).message); }
    }
    void load();
    const interval = setInterval(() => void load(), 2000);
    return () => { cancelled = true; clearInterval(interval); };
  }, [tab, task.id, session?.id]);

  async function attach(selected: Session) {
    await perform(async () => {
      const result = await post<{ opened: boolean; command: string }>(`/tasks/${task.id}/sessions/${selected.id}/attach`);
      if (!result.opened) { await copyText(result.command); notify('attach 命令已复制，请在终端执行'); }
    });
  }

  async function send() {
    if (!session || !message.trim()) return;
    if (await perform(() => post(`/tasks/${task.id}/sessions/${session.id}/chat`, { message }))) setMessage('');
  }

  async function changeTaskProjectDir() {
    const dir = await chooseFolder('选择项目目录').catch(() => undefined);
    if (!dir) return;
    await perform(async () => {
      await selectProject(dir);
      await api(`/tasks/${task.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ projectDir: dir }),
      });
    });
  }

  const openFile = (fileId: string) => void perform(() => post(`/tasks/${task.id}/open`, { fileId }));
  const stateLabel = { todo: '待运行', doing: '执行中', waiting: '待确认', done: '已完成' }[task.status];

  return <>
    <div className="drawer-heading"><div className="detail-header-main"><div className="detail-header-status"><span className={`task-status ${task.status}`}><i />{task.status} · {stateLabel}</span><span>{task.oneShot ? '一次性任务' : '持续交流'}</span><span>创建于 {date(task.createdAt)}</span><span title={task.id}>#{task.id.slice(0, 8)}</span></div><h2>{task.title}</h2></div><button className="icon-button close-button" aria-label="关闭任务详情" onClick={close}><X size={19} /></button></div>
    <nav className="detail-tabs" aria-label="任务详情">{([{ id: 'overview', label: '概览' }, { id: 'sessions', label: '对话与日志', count: task.sessions.filter(s => s.kind === 'work').length }, { id: 'diff', label: 'Git 与确认' }, { id: 'archive', label: '归档' }] as const).map(item => <button key={item.id} className={tab === item.id ? 'active' : ''} onClick={() => { setTab(item.id); setEditing(false); setError(''); }} aria-current={tab === item.id ? 'page' : undefined}>{item.label}{'count' in item && item.count > 0 && <span>{item.count}</span>}</button>)}</nav>
    <div className={`drawer-content ${editing && task.status !== 'done' ? 'is-wide-editor' : ''}`}>
      {(error || task.error) && !editing && <div role="alert" className="inline-error">{error || task.error}</div>}
      {editing ? <TaskEditor wide={task.status !== 'done'} task={task} health={health} defaultProjectDir={defaultProjectDir} projectDirs={projectDirs} onProjectsChange={() => void refresh()} onSaved={() => { setEditing(false); void refresh(); }} /> : <>
        {tab === 'overview' && <>
          {task.oneShot && task.status === 'done' ? <>
            <div className="section-heading first">
              <h3>回答</h3>
              {workSessions.length > 0 && (
                <button className="text-button" onClick={() => setTab('sessions')}>
                  查看完整对话 <ArrowUpRight size={13} />
                </button>
              )}
            </div>
            {latestAnswer ? (
              <Markdown text={latestAnswer.text} />
            ) : (
              <p className="muted empty-line">暂无回答记录，可前往「对话与日志」查看运行日志。</p>
            )}
            <div className="section-heading"><h3>任务需求</h3></div>
            <Markdown text={task.description || '暂未填写详细需求。'} />
          </> : <>
            <div className="section-heading first"><h3>任务需求</h3>{['todo', 'waiting'].includes(task.status) && <button className="text-button" onClick={() => setEditing(true)}>编辑任务</button>}</div>
            <Markdown text={task.description || '暂未填写详细需求。'} />
          </>}
          <dl className="properties">
            <div><dt>Agent 工具</dt><dd>{toolLabels[task.tool]} <span className="muted">/ {task.mode}</span></dd></div>
            <div>
              <dt>项目目录</dt>
              <dd className="path">
                <span>{effectiveProjectDir || '未选择项目目录'}</span>
                {effectiveProjectDir && (
                  <button
                    type="button"
                    className="text-button"
                    style={{ marginLeft: 10 }}
                    onClick={() => void openFolder(effectiveProjectDir).catch(err => setError((err as Error).message))}
                  >
                    打开目录
                  </button>
                )}
                {['todo', 'waiting'].includes(task.status) && (
                  <button
                    type="button"
                    className="text-button"
                    style={{ marginLeft: 10 }}
                    onClick={() => void changeTaskProjectDir()}
                  >
                    切换目录…
                  </button>
                )}
              </dd>
            </div>
            <div><dt>归档目录</dt><dd className="path">{task.archivePath || DEFAULT_ARCHIVE_PATH}</dd></div>
          </dl>
          <div className="section-heading"><h3>输入文件 <span>{task.input.length}</span></h3></div><Files taskId={task.id} files={task.input} open={openFile} editMarkdown={file => setDocModal({ taskId: task.id, fileId: file.id, title: file.name })} />
          <div className="section-heading"><h3>输出文件 <span>{task.output.length}</span></h3></div><Files taskId={task.id} files={task.output} open={openFile} editMarkdown={file => setDocModal({ taskId: task.id, fileId: file.id, title: file.name })} />
          {task.sessions.some(s => s.kind === 'work') && <><div className="section-heading"><h3>Sessions</h3><button className="text-button" onClick={() => setTab('sessions')}>查看对话 <ArrowUpRight size={13} /></button></div><div className="overview-sessions">{task.sessions.filter(s => s.kind === 'work').map(s => <button key={s.id} onClick={() => { setActiveSession(s.id); setTab('sessions'); }}><SessionRow session={s} now={now} /></button>)}</div></>}
          <div className="detail-actions">{task.status === 'todo' && <button className="button primary" disabled={busy} onClick={() => void perform(() => post(`/tasks/${task.id}/run`, { projectDir: effectiveProjectDir }))}><Play size={14} />运行任务</button>}{task.status === 'waiting' && <><button className="button primary" disabled={busy} onClick={async () => {
            setBusy(true); setError('');
            try {
              await post(`/tasks/${task.id}/move`, { status: 'done' });
              await refresh();
              setTab('archive');
            } catch (err) {
              const msg = (err as Error).message;
              if (msg.includes('查看 diff')) setTab('diff');
              else setError(msg);
            } finally {
              setBusy(false);
            }
          }}><Check size={14} />确认完成</button><button className="button" onClick={() => setTab('sessions')}><MessageSquare size={14} />继续交流</button></>}{task.status === 'doing' && <button className="button" onClick={() => setTab('sessions')}><Terminal size={14} />查看运行日志</button>}{task.status === 'done' && <><button className="button" onClick={() => setTab('archive')}><FileText size={14} />查看归档</button><button className="button" disabled={busy} onClick={() => void perform(async () => { if (task.archive.status !== 'done' && task.archive.status !== 'running') await post(`/tasks/${task.id}/archive`); await setTaskArchived(task.id, !task.archived); })}>{task.archived ? <ArchiveRestore size={14} /> : <Archive size={14} />}{task.archived ? '取消归档' : '归档任务'}</button><button className="button danger" disabled={busy} onClick={() => { void confirmAction(`确认删除任务「${task.title}」？`, '删除后任务记录、运行日志与本地副本将被移除，且不可恢复。', '删除任务').then(confirmed => { if (!confirmed) return; void perform(async () => { await removeTask(task.id); close(); }); }); }}><Trash2 size={14} />删除任务</button></>}</div>
        </>}

        {tab === 'sessions' && <>
          <div className="section-heading first"><h3>任务 Sessions</h3>{task.status !== 'done' && <button className="text-button" onClick={() => setAdding(!adding)}><Plus size={14} />再开一个 Agent</button>}</div>
          {adding && <div className="add-session"><label>工具<select value={tool} onChange={event => setTool(event.target.value as Tool)}>{ALL_TOOLS.map(tool => <option key={tool} value={tool}>{toolLabels[tool]}</option>)}</select></label><label>模式<select value={mode} disabled={task.oneShot} onChange={event => setMode(event.target.value as Mode)}><option value="headless">headless</option><option value="tmux">tmux</option></select></label><button className="button primary" disabled={busy} onClick={async () => {
            if (await perform(async () => { const s = await post<Session>(`/tasks/${task.id}/run`, { tool, mode: task.oneShot ? 'headless' : mode, projectDir: effectiveProjectDir }); setActiveSession(s.id); })) setAdding(false);
          }}><Play size={13} />启动</button></div>}
          {!task.sessions.length && <div className="detail-empty"><Terminal size={25} /><p>任务还没有运行记录</p></div>}
          <div className="session-selector">{task.sessions.map(s => <button key={s.id} className={session?.id === s.id ? 'selected' : ''} onClick={() => setActiveSession(s.id)}><SessionRow session={s} now={now} />{s.kind === 'archive' && <small>归档 Agent</small>}</button>)}</div>
          {session && <>
            <div className="session-toolbar"><span title={session.nativeId || session.id}>#{session.id.slice(0, 8)} {session.nativeId ? '· 上下文已保存' : '· 等待工具建立会话'}</span><div>{task.status !== 'done' && session.kind === 'work' && (session.mode === 'tmux' || session.status === 'ended') && <button className="button small" disabled={busy || (!session.nativeId && session.mode === 'headless')} onClick={() => void attach(session)}><Terminal size={13} />attach</button>}{session.status !== 'ended' && <button className="button small danger" disabled={busy} onClick={() => void perform(() => post(`/tasks/${task.id}/sessions/${session.id}/stop`))}><Square size={11} />停止</button>}</div></div>
            {session.mode === 'tmux' && session.tool === 'codex' && <p className="context-note">首次使用请 attach，在 Codex 中通过 <code>/hooks</code> 信任看板回报 Hook。之后，终端输入与每轮停止会同步到看板。</p>}
            {session.error && <div className="inline-error">{session.error}</div>}
            <div className="conversation">{session.messages.length ? session.messages.map(item => <article key={item.id} className={`message ${item.role}`}><div className="message-meta"><strong>{item.role === 'user' ? '你' : toolLabels[session.tool]}</strong><time>{date(item.at)}</time></div><Markdown text={item.text} /></article>) : <div className="muted conversation-empty">{session.status === 'running' ? 'Agent 正在处理任务，回复会自动显示。' : '暂无对话内容，可查看下方日志。'}</div>}</div>
            {session.kind === 'work' && task.status !== 'done' && <div className="chat-composer"><textarea aria-label="发给 Agent 的消息" placeholder={session.status === 'running' ? 'Agent 正在运行，等待本轮结束后可继续交流' : '补充要求或继续交流…'} rows={3} value={message} disabled={busy || session.status === 'running' || (session.status === 'ended' && !session.nativeId)} onChange={event => setMessage(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void send(); } }} /><div><small>⌘ / Ctrl + Enter 发送</small><button className="button primary small" disabled={busy || !message.trim() || session.status === 'running'} onClick={() => void send()}><Send size={13} />发送</button></div></div>}
            <details className="log-details" open={!session.messages.length}><summary>运行日志 <span>自动刷新 · 最近 160 KB</span></summary>{logError && <p className="inline-error">{logError}</p>}<pre className="log-output">{logs || '等待日志输出…'}</pre></details>
          </>}
        </>}

        {tab === 'diff' && <>
          <div className="section-heading first"><h3>改动与提交</h3><button className="text-button" disabled={reviewLoading || busy} onClick={() => void loadReview()}><RefreshCw size={13} className={reviewLoading ? 'spin' : ''} />刷新 diff</button></div>
          {task.git && task.git.status !== 'error' && <div className="git-result"><GitBranch size={16} /><div><strong>{task.git.status === 'committed' ? `${task.git.automatic ? '自动' : '手动'}提交 · ${task.git.hash?.slice(0, 8)}` : task.git.message}</strong>{task.git.status === 'committed' && <p>{task.git.message}</p>}{task.git.url && <a href={task.git.url} onClick={event => { event.preventDefault(); void openExternal(task.git!.url!); }}>查看 commit <ArrowUpRight size={12} /></a>}{task.git.prUrl && <a href={task.git.prUrl} onClick={event => { event.preventDefault(); void openExternal(task.git!.prUrl!); }}>查看 PR <ArrowUpRight size={12} /></a>}</div></div>}
          {reviewError && <div role="alert" className="inline-error">{reviewError}</div>}
          {reviewLoading && !review && <div className="loading-line"><LoaderCircle size={16} className="spin" />读取 Git 改动…</div>}
          {review && <>
            {review.available ? <><div className="repo-heading"><GitBranch size={14} /><strong>{review.branch || 'detached HEAD'}</strong><code>{review.head?.slice(0, 8)}</code><span>{review.files.length} 个文件</span></div><p className="repo-path">{review.root}</p>{review.excluded.length > 0 && <p className="context-note">运行前已有的改动会保留：{review.excluded.join('、')}</p>}{review.blocked && <div className="inline-error">{review.blocked}</div>}{review.diff ? <pre className="diff-output">{review.diff.split('\n').map((line, index) => <span key={index} className={line.startsWith('+') ? 'added' : line.startsWith('-') ? 'removed' : line.startsWith('@@') ? 'hunk' : ''}>{line || ' '}<br /></span>)}</pre> : <div className="detail-empty small-empty"><Check size={21} /><p>当前没有需要提交的改动</p></div>}</> : <p className="context-note">工作目录未使用 Git。确认后会直接生成 Obsidian 归档。</p>}
            {task.status === 'waiting' && <div className="confirmation">{review.available && review.files.length > 0 && <label className="field"><span>Commit 信息</span><textarea rows={3} value={commitMessage} onChange={event => setCommitMessage(event.target.value)} disabled={busy} /></label>}<div className="confirmation-actions"><button className="button primary" disabled={busy || reviewLoading} onClick={async () => {
              if (await perform(() => post(`/tasks/${task.id}/complete`, { token: review.token, action: 'none', message: commitMessage }))) setTab('archive');
              else void loadReview();
            }}><Check size={14} />{busy ? '处理中…' : '确认并归档'}</button>{review.available && review.files.length > 0 && <button className="button" disabled={busy || reviewLoading || !!review.blocked || !commitMessage.trim()} onClick={async () => {
              if (await perform(() => post(`/tasks/${task.id}/complete`, { token: review.token, action: 'commit', message: commitMessage }))) setTab('archive');
              else void loadReview();
            }}><GitBranch size={14} />提交未暂存改动并完成</button>}{review.available && review.files.length > 0 && <button className="button" disabled={busy || reviewLoading || !!review.blocked || !commitMessage.trim() || !health?.tools.gh.available} title={!health?.tools.gh.available ? '需要安装并登录 gh' : '将推送当前任务分支并创建或关联 PR'} onClick={async () => {
              if (await perform(() => post(`/tasks/${task.id}/complete`, { token: review.token, action: 'pr', message: commitMessage }))) setTab('archive');
              else void loadReview();
            }}><GitBranch size={14} />提交并创建 PR</button>}</div></div>}
            {task.status === 'doing' && <p className="field-hint">Agent 仍在执行。全部停止后可以确认完成。</p>}
          </>}
        </>}

        {tab === 'archive' && <>
          <div className="section-heading first"><h3>Obsidian 归档</h3></div>
          {task.status !== 'done' ? <div className="detail-empty"><FileText size={27} /><p>任务完成后自动归档</p></div> : <div className="archive-state">
            {task.archive.status === 'running' ? <LoaderCircle size={22} className="spin" /> : task.archive.status === 'done' ? <Check size={22} /> : <FileText size={22} />}
            <div><strong>{task.archive.status === 'running' ? '归档 Agent 正在整理记录' : task.archive.status === 'done' ? '已写入 Obsidian 笔记' : task.archive.status === 'error' ? '归档未完成' : '等待归档'}</strong>{task.archive.error && <p>{task.archive.error}</p>}</div>
          </div>}
          <dl className="properties"><div><dt>归档目录</dt><dd className="path">{task.archivePath || DEFAULT_ARCHIVE_PATH}</dd></div>{task.archive.path && <div><dt>笔记路径</dt><dd className="path">{task.archive.path}</dd></div>}</dl>
          <div className="detail-actions">{task.archive.status === 'done' && <><button className="button primary" disabled={busy} onClick={() => setDocModal({ taskId: task.id, archive: true, title: task.title + ' · 归档笔记' })}><Pencil size={14} />编辑笔记</button><button className="button" disabled={busy} onClick={() => void perform(() => post(`/tasks/${task.id}/open`, { archive: true }))}><FolderOpen size={14} />外部打开</button><a className="button" href={`/api/tasks/${task.id}/archive`}><Download size={14} />下载 Markdown</a><button className="icon-button" title="复制笔记路径" aria-label="复制笔记路径" onClick={() => { void copyText(task.archive.path!).then(() => notify('笔记路径已复制'), () => setError('复制失败，请手动复制路径。')); }}><Clipboard size={15} /></button></>}{task.archive.status === 'error' && <><button className="button primary" disabled={busy} onClick={() => void perform(() => post(`/tasks/${task.id}/archive`))}><RefreshCw size={14} />重试归档</button><button className="button" onClick={() => setEditing(true)}>修改归档目录</button></>}</div>
          {task.sessions.some(s => s.kind === 'archive') && <button className="text-button" onClick={() => { setActiveSession(task.sessions.filter(s => s.kind === 'archive').at(-1)!.id); setTab('sessions'); }}>查看归档 Agent 日志 <ArrowUpRight size={13} /></button>}
        </>}
      </>}
    </div>
    {docModal && <MarkdownDocumentModal target={docModal} onClose={() => setDocModal(null)} notify={notify} onSaved={() => void refresh()} />}
  </>;
}
