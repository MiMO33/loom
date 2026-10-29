import { useEffect, useRef, useState } from 'react';
import { FilePlus2, Folder, FolderOpen, ImagePlus, Play, X } from 'lucide-react';
import type { Health, Mode, Settings, Task, TaskFile, Tool } from '../shared/types';
import { ALL_TOOLS, DEFAULT_ARCHIVE_PATH, toolLabels } from '../shared/types';
import { api, chooseFolder, post, shortPath } from './api';
import { ToolMark } from './components';
import { OrcaMarkdownEditor } from './markdown-editor/OrcaMarkdownEditor';

interface Props {
  task?: Task;
  health?: Health;
  settings?: Settings;
  wide?: boolean;
  defaultProjectDir?: string;
  projectDirs?: string[];
  onProjectsChange?: () => void;
  onSaved: (task: Task) => void;
}
interface PendingFile { id: string; file: File; previewUrl?: string }

export function TaskEditor({
  task,
  health,
  settings,
  wide = false,
  defaultProjectDir = '',
  projectDirs = [],
  onProjectsChange,
  onSaved,
}: Props) {
  const defaultOneShot = task?.oneShot ?? settings?.defaultOneShot ?? true;
  const [draft, setDraft] = useState({
    title: task?.title || '',
    description: task?.description || '',
    tool: (task?.tool || settings?.defaultTool || 'claude') as Tool,
    mode: (task?.mode || (defaultOneShot ? 'headless' : settings?.defaultMode) || 'headless') as Mode,
    oneShot: defaultOneShot,
    projectDir: task?.projectDir || defaultProjectDir || projectDirs[0] || '',
    archivePath: task?.archivePath || settings?.defaultArchivePath || DEFAULT_ARCHIVE_PATH,
  });
  const [saved, setSaved] = useState(task);
  const [pending, setPending] = useState<PendingFile[]>([]);
  const pendingRef = useRef<PendingFile[]>([]);
  pendingRef.current = pending;
  const [existing, setExisting] = useState(task?.input || []);
  const [recentDirs, setRecentDirs] = useState<string[]>(projectDirs);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [dragOver, setDragOver] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const archiveOnly = task?.status === 'done';
  const patch = (value: Partial<typeof draft>) => setDraft(current => ({ ...current, ...value }));

  useEffect(() => {
    return () => {
      pendingRef.current.forEach(item => {
        if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
      });
    };
  }, []);

  useEffect(() => {
    setRecentDirs(projectDirs);
  }, [projectDirs]);

  useEffect(() => {
    if (!draft.projectDir && (defaultProjectDir || projectDirs[0])) {
      const next = defaultProjectDir || projectDirs[0] || '';
      if (next) {
        setDraft(current => (current.projectDir ? current : { ...current, projectDir: next }));
      }
    }
  }, [defaultProjectDir, projectDirs, draft.projectDir]);

  async function selectProjectDir(dir: string) {
    const trimmed = dir.trim();
    patch({ projectDir: trimmed });
    if (!trimmed) return;
    try {
      const res = await post<{ lastProjectDir: string; projectDirs: string[] }>('/projects', { path: trimmed });
      setRecentDirs(res.projectDirs || []);
      if (res.lastProjectDir) {
        patch({ projectDir: res.lastProjectDir });
      }
      if (saved && saved.status !== 'done') {
        const updated = await api<Task>(`/tasks/${saved.id}`, {
          method: 'PATCH',
          body: JSON.stringify({ projectDir: res.lastProjectDir || trimmed }),
        });
        setSaved(updated);
      }
      onProjectsChange?.();
    } catch {
      // Ignore if typed path does not exist yet
    }
  }

  async function removeRecentProjectDir(dir: string) {
    try {
      const res = await api<{ lastProjectDir: string; projectDirs: string[] }>('/projects', {
        method: 'DELETE',
        body: JSON.stringify({ path: dir }),
      });
      setRecentDirs(res.projectDirs || []);
      if (draft.projectDir === dir) {
        patch({ projectDir: res.lastProjectDir || '' });
      }
      onProjectsChange?.();
    } catch (err) {
      setError((err as Error).message);
    }
  }

  function resolveImageSrc(src: string): string {
    if (src.startsWith('attachment:')) {
      const id = src.slice('attachment:'.length);
      const found = pendingRef.current.find(item => item.id === id);
      if (found?.previewUrl) return found.previewUrl;
    }
    return src;
  }

  function stage(files: File[], embed = false) {
    if (files.some(file => file.size > 20 * 1024 * 1024)) {
      setError('每个文件不超过 20 MB。');
      return;
    }
    const incoming: PendingFile[] = files.map(file => ({
      file,
      id: crypto.randomUUID(),
      previewUrl: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
    }));
    const nextPending = [...pendingRef.current, ...incoming];
    pendingRef.current = nextPending;
    setPending(nextPending);
    if (embed) {
      setDraft(current => ({
        ...current,
        description:
          current.description +
          '\n' +
          incoming.map(item => `![${item.file.name}](attachment:${item.id})`).join('\n') +
          '\n',
      }));
    }
  }

  async function save(run: boolean) {
    setError('');
    if (!draft.title.trim()) {
      setError('请输入任务名。');
      return;
    }
    const effectiveProjectDir = draft.projectDir.trim() || defaultProjectDir || recentDirs[0] || '';
    if (!archiveOnly && !effectiveProjectDir) {
      setError('请选择项目目录。');
      return;
    }
    setBusy(true);
    try {
      const payload = { ...draft, projectDir: effectiveProjectDir };
      let result = await api<Task>(saved ? `/tasks/${saved.id}` : '/tasks', {
        method: saved ? 'PATCH' : 'POST',
        body: JSON.stringify(payload),
      });
      setSaved(result);
      if (pending.length) {
        const data = new FormData();
        pending.forEach(item => data.append('files', item.file));
        const uploaded = await api<TaskFile[]>(`/tasks/${result.id}/files`, {
          method: 'POST',
          body: data,
        });
        let description = draft.description;
        pending.forEach((item, index) => {
          description = description.replaceAll(
            `attachment:${item.id}`,
            `/api/tasks/${result.id}/files/${uploaded[index].id}`,
          );
          if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
        });
        pendingRef.current = [];
        setPending([]);
        setExisting(current => [...current, ...uploaded]);
        patch({ description });
        result = await api<Task>(`/tasks/${result.id}`, {
          method: 'PATCH',
          body: JSON.stringify({ description }),
        });
        setSaved(result);
      }
      if (run) {
        await post(`/tasks/${result.id}/run`, { projectDir: result.projectDir || effectiveProjectDir });
        result = { ...result, status: 'doing' };
      }
      onSaved(result);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  const sidebarControls = (
    <div className="editor-inspector-fields">
      {!archiveOnly && (
        <>
          <fieldset className="field">
            <legend>Agent 工具</legend>
            <div className="tool-picker">
              {ALL_TOOLS.map(tool => (
                <label key={tool} className={draft.tool === tool ? 'selected' : ''}>
                  <input
                    type="radio"
                    name="tool"
                    value={tool}
                    checked={draft.tool === tool}
                    onChange={() => patch({ tool })}
                  />
                  <ToolMark tool={tool} />
                  <span>{toolLabels[tool]}</span>
                </label>
              ))}
            </div>
            {health && health.tools[draft.tool] && !health.tools[draft.tool].available && (
              <small className="field-hint warning">
                本机暂未检测到 {toolLabels[draft.tool]}，安装后可刷新环境状态。
              </small>
            )}
          </fieldset>

          <div className="run-options">
            <label className="checkbox">
              <input
                type="checkbox"
                checked={draft.oneShot}
                onChange={event =>
                  patch({
                    oneShot: event.target.checked,
                    ...(event.target.checked ? { mode: 'headless' as Mode } : {}),
                  })
                }
              />
              <span>
                一次性任务
                <small>全部结束后自动提交并归档</small>
              </span>
            </label>
            <label className="mode-select">
              运行模式
              <select
                value={draft.mode}
                disabled={draft.oneShot}
                onChange={event => patch({ mode: event.target.value as Mode })}
              >
                <option value="headless">headless · 后台</option>
                <option value="tmux">tmux · 交互终端</option>
              </select>
            </label>
          </div>

          <div className="field">
            <div className="field-heading">
              <span>
                项目目录 <b>*</b>
              </span>
            </div>
            <div className="path-input-row">
              <input
                placeholder="例如 ~/code/my-project"
                value={draft.projectDir}
                onChange={event => patch({ projectDir: event.target.value })}
                onBlur={event => {
                  const val = event.target.value.trim();
                  if (val) void selectProjectDir(val);
                }}
              />
              <button
                type="button"
                className="button small"
                onClick={() =>
                  void chooseFolder('选择项目目录')
                    .then(dir => {
                      if (dir) void selectProjectDir(dir);
                    })
                    .catch(() => undefined)
                }
              >
                <FolderOpen size={13} />
                选择…
              </button>
            </div>
            {recentDirs.length > 0 && (
              <div className="recent-project-dirs" aria-label="常用项目目录">
                {recentDirs.map(dir => (
                  <div
                    key={dir}
                    className={`project-dir-chip ${draft.projectDir === dir ? 'active' : ''}`}
                  >
                    <button
                      type="button"
                      className="project-dir-pick"
                      title={dir}
                      onClick={() => void selectProjectDir(dir)}
                    >
                      <Folder size={11} />
                      <span>{shortPath(dir)}</span>
                    </button>
                    <button
                      type="button"
                      className="project-dir-remove"
                      title={`移除常用目录 ${dir}`}
                      aria-label={`移除 ${dir}`}
                      onClick={() => void removeRecentProjectDir(dir)}
                    >
                      <X size={11} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}

      <label className="field">
        <span>Obsidian 归档目录</span>
        <div className="path-input-row">
          <input
            placeholder={DEFAULT_ARCHIVE_PATH}
            value={draft.archivePath}
            onChange={event => patch({ archivePath: event.target.value })}
          />
          <button
            type="button"
            className="button small"
            onClick={() =>
              void chooseFolder('选择 Obsidian 归档目录')
                .then(dir => {
                  if (dir) patch({ archivePath: dir });
                })
                .catch(() => undefined)
            }
          >
            <FolderOpen size={13} />
            选择…
          </button>
        </div>
      </label>

      {!archiveOnly && (
        <div className="field">
          <span>输入文件与图片附件</span>
          <button
            type="button"
            className={`upload-zone ${dragOver ? 'dragover' : ''}`}
            onClick={() => fileInput.current?.click()}
            onDragOver={event => {
              event.preventDefault();
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={event => {
              event.preventDefault();
              setDragOver(false);
              stage(Array.from(event.dataTransfer.files));
            }}
          >
            <FilePlus2 size={16} />
            <span>拖入文件或点击选择</span>
            <small>≤ 20 MB</small>
          </button>
          <input
            type="file"
            multiple
            hidden
            ref={fileInput}
            onChange={event => {
              stage(Array.from(event.target.files || []));
              event.target.value = '';
            }}
          />
          {(existing.length > 0 || pending.length > 0) && (
            <div className="upload-list">
              {existing.map(file => (
                <div key={file.id}>
                  <ImagePlus size={13} />
                  <span>{file.name}</span>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`移除 ${file.name}`}
                    onClick={async () => {
                      if (!saved) return;
                      try {
                        await api(`/tasks/${saved.id}/files/${file.id}`, { method: 'DELETE' });
                        setExisting(current => current.filter(item => item.id !== file.id));
                      } catch (err) {
                        setError((err as Error).message);
                      }
                    }}
                  >
                    <X size={13} />
                  </button>
                </div>
              ))}
              {pending.map(item => (
                <div key={item.id}>
                  <ImagePlus size={13} />
                  <span>{item.file.name}</span>
                  <small>待保存</small>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={`移除 ${item.file.name}`}
                    onClick={() => {
                      if (item.previewUrl) URL.revokeObjectURL(item.previewUrl);
                      const nextPending = pendingRef.current.filter(file => file.id !== item.id);
                      pendingRef.current = nextPending;
                      setPending(nextPending);
                      patch({
                        description: draft.description.replaceAll(
                          `![${item.file.name}](attachment:${item.id})`,
                          '',
                        ),
                      });
                    }}
                  >
                    <X size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );

  return (
    <form
      className={`task-editor ${wide && !archiveOnly ? 'task-editor-wide' : ''}`}
      onSubmit={event => {
        event.preventDefault();
        void save(false);
      }}
    >
      {error && (
        <div role="alert" className="inline-error">
          {error}
        </div>
      )}

      {wide && !archiveOnly ? (
        <div className="task-editor-workbench">
          <div className="editor-writing-pane">
            <input
              autoFocus={!task}
              className="editor-document-title"
              maxLength={200}
              placeholder="输入任务标题…"
              value={draft.title}
              onChange={event => patch({ title: event.target.value })}
              disabled={busy}
            />
            <div className="editor-document-canvas">
              <OrcaMarkdownEditor
                value={draft.description}
                onChange={description => patch({ description })}
                placeholder="输入任务需求，输入 / 插入块，可直接粘贴图片…"
                disabled={busy}
                onSave={() => void save(false)}
                onPasteOrDropFiles={files => stage(files, true)}
                resolveImageSrc={resolveImageSrc}
              />
            </div>
          </div>
          <aside className="editor-inspector-pane">{sidebarControls}</aside>
        </div>
      ) : (
        <>
          {!archiveOnly && (
            <>
              <label className="field">
                <span>
                  任务名 <b>*</b>
                </span>
                <input
                  autoFocus={!task}
                  maxLength={200}
                  placeholder="输入任务标题…"
                  value={draft.title}
                  onChange={event => patch({ title: event.target.value })}
                  disabled={busy}
                />
              </label>
              <div className="field description-field">
                <div className="field-heading">
                  <label>详细需求</label>
                </div>
                <OrcaMarkdownEditor
                  value={draft.description}
                  onChange={description => patch({ description })}
                  placeholder="输入任务需求，输入 / 插入块，可直接粘贴图片…"
                  disabled={busy}
                  onSave={() => void save(false)}
                  onPasteOrDropFiles={files => stage(files, true)}
                  resolveImageSrc={resolveImageSrc}
                />
              </div>
            </>
          )}
          {sidebarControls}
        </>
      )}

      <div className="editor-footer">
        <span className="muted">
          {busy ? '正在保存…' : saved ? '⌘S 快速保存' : ''}
        </span>
        <button type="submit" className="button" disabled={busy}>
          {saved ? '保存修改' : '放入 todo'}
        </button>
        {!archiveOnly && (
          <button
            type="button"
            className="button primary"
            disabled={busy}
            onClick={() => void save(true)}
          >
            <Play size={14} />
            {saved ? '保存并运行' : '直接运行'}
          </button>
        )}
      </div>
    </form>
  );
}
