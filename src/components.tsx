import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Download, ExternalLink, File, FileText, Pencil, Terminal } from 'lucide-react';
import { toolLabels, statusLabels, type Session, type TaskFile, type Tool } from '../shared/types';
import { duration } from './api';

export function ToolMark({ tool }: { tool: Tool }) {
  return (
    <span className={`tool-mark ${tool}`} aria-hidden="true">
      {tool === 'claude' && (
        <svg viewBox="0 0 24 24" fill="currentColor" className="tool-mark-svg">
          <path d="M11.12 2h1.76l.65 6.82 5.28-4.38 1.24 1.24-4.38 5.28L22.5 11.6v1.76l-6.83.64 4.38 5.29-1.24 1.24-5.28-4.38-.65 6.85h-1.76l-.64-6.85-5.29 4.38-1.24-1.24 4.38-5.29L1.5 13.36V11.6l6.83-.64-4.38-5.28 1.24-1.24 5.29 4.38L11.12 2Z" />
        </svg>
      )}
      {tool === 'codex' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" className="tool-mark-svg">
          <path d="M12 2.5a4.8 4.8 0 0 0-4.16 2.4 4.8 4.8 0 0 0-4.36 2.2A4.8 4.8 0 0 0 4.5 13.5a4.8 4.8 0 0 0 .82 4.8 4.8 4.8 0 0 0 6.68 1.2 4.8 4.8 0 0 0 4.16 2 4.8 4.8 0 0 0 4.36-2.2 4.8 4.8 0 0 0-1.02-6.8 4.8 4.8 0 0 0-.82-4.8A4.8 4.8 0 0 0 12 2.5Z" />
          <path d="M9 9.5v6.5l5.6-3.25" />
          <path d="M15 14.5V8l-5.6 3.25" />
        </svg>
      )}
      {tool === 'pi' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" className="tool-mark-svg">
          <path d="M5 7h14" />
          <path d="M9.5 7v8.5a2.5 2.5 0 0 1-2.5 2.5" />
          <path d="M15 7v8a2.5 2.5 0 0 0 2.5 2.5c.7 0 1.2-.2 1.5-.5" />
        </svg>
      )}
      {tool === 'gemini' && (
        <svg viewBox="0 0 24 24" fill="currentColor" className="tool-mark-svg">
          <path d="M12 2C12 7.52 7.52 12 2 12c5.52 0 10 4.48 10 10 0-5.52 4.48-10 10-10-5.52 0-10-4.48-10-10Z" />
        </svg>
      )}
      {tool === 'opencode' && (
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1" strokeLinecap="round" strokeLinejoin="round" className="tool-mark-svg">
          <rect x="3" y="4" width="18" height="16" rx="3.5" />
          <path d="m8 9.5 3 2.5-3 2.5" />
          <path d="M13 14.5h3" />
        </svg>
      )}
      {tool === 'cursor' && (
        <svg viewBox="0 0 24 24" fill="currentColor" className="tool-mark-svg">
          <path d="M12 2 3.34 7v10L12 22l8.66-5V7L12 2Zm0 2.31 6.4 3.7-6.4 3.7-6.4-3.7 6.4-3.7Zm-7 5.14 6.2 3.58v7.15L5 16.6V9.45Zm14 7.15-6.2 3.58v-7.15L19 9.45v7.15Z" />
        </svg>
      )}
    </span>
  );
}

export function SessionRow({ session, now, compact = false, onAttach }: { session: Session; now: number; compact?: boolean; onAttach?: () => void }) {
  const elapsed = session.activeMs + (session.status === 'running' ? Math.max(0, now - Date.parse(session.stateSince)) : 0);
  return <div className={`session-row ${compact ? 'compact' : ''}`}>
    <ToolMark tool={session.tool} /><span className="session-tool" title={toolLabels[session.tool]}>{toolLabels[session.tool]}</span>
    <span className="mode-label">{session.mode}</span>
    <span className={`session-state ${session.error ? 'failed' : session.status}`}><i />{session.error ? '执行失败' : statusLabels[session.status]}</span>
    <time title="累计执行时长，不含等待输入">{duration(elapsed)}</time>
    {onAttach && <button className="icon-button row-attach" aria-label={`attach ${toolLabels[session.tool]} session`} title="attach tmux 会话" onClick={event => { event.stopPropagation(); onAttach(); }}><Terminal size={12} /></button>}
  </div>;
}

export function Markdown({ text, resolveImageSrc }: { text: string; resolveImageSrc?: (src: string) => string }) {
  return <div className="markdown"><ReactMarkdown
    remarkPlugins={[remarkGfm]}
    urlTransform={(url, key) => {
      if (key === 'src' && (url.startsWith('attachment:') || url.startsWith('blob:') || url.startsWith('data:'))) {
        return url;
      }
      return defaultUrlTransform(url);
    }}
    components={{
      a: props => <a {...props} target="_blank" rel="noreferrer" />,
      img: ({ src, alt, ...props }) => {
        const resolvedSrc = src && resolveImageSrc ? resolveImageSrc(src) : src;
        return <img {...props} src={resolvedSrc} loading="lazy" alt={alt || '需求图片'} />;
      },
    }}
  >{text}</ReactMarkdown></div>;
}

export function Files({ files, taskId, open, editMarkdown, remove }: { files: TaskFile[]; taskId: string; open: (id: string) => void; editMarkdown?: (file: TaskFile) => void; remove?: (id: string) => void }) {
  if (!files.length) return <p className="muted empty-line">暂无文件</p>;
  return <div className="file-list">{files.map(file => {
    const isMd = /\.(md|markdown|txt)$/i.test(file.name);
    return <div className="file-row" key={file.id}>
      {isMd ? <FileText size={17} /> : <File size={17} />}
      <div className="file-info"><span>{file.name}</span><small title={file.path}>{file.path}</small></div>
      <span className="file-size">{new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(file.size / 1024)} KB</span>
      {isMd && editMarkdown && <button className="icon-button" title="在 Loom Markdown 编辑器中打开" aria-label={`编辑 ${file.name}`} onClick={() => editMarkdown(file)}><Pencil size={14} /></button>}
      <button className="icon-button" title="在本机打开" aria-label={`打开 ${file.name}`} onClick={() => open(file.id)}><ExternalLink size={14} /></button>
      <a className="icon-button" title="下载文件" aria-label={`下载 ${file.name}`} href={`/api/tasks/${taskId}/files/${file.id}`}><Download size={14} /></a>
      {remove && <button className="text-button danger" onClick={() => remove(file.id)}>移除</button>}
    </div>;
  })}</div>;
}
