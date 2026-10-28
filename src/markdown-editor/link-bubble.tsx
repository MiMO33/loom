import React, { useEffect, useRef, useState } from 'react';
import type { Editor } from '@tiptap/react';
import { Check, Copy, ExternalLink, Pencil, Trash2, X } from 'lucide-react';
import { copyText, openExternal } from '../api';

export type LinkBubbleState = {
  href: string;
  left: number;
  top: number;
};

export function getLinkBubblePosition(
  editor: Editor,
  rootEl: HTMLElement | null,
): { left: number; top: number } | null {
  if (!rootEl) return null;
  try {
    const { from } = editor.state.selection;
    const coords = editor.view.coordsAtPos(from);
    const rect = rootEl.getBoundingClientRect();
    return {
      left: Math.max(8, coords.left - rect.left),
      top: coords.bottom - rect.top + 6,
    };
  } catch {
    return null;
  }
}

export function RichMarkdownLinkBubble({
  linkBubble,
  isEditing,
  onSave,
  onRemove,
  onEditStart,
  onEditCancel,
}: {
  linkBubble: LinkBubbleState;
  isEditing: boolean;
  onSave: (href: string) => void;
  onRemove: () => void;
  onEditStart: () => void;
  onEditCancel: () => void;
}): React.JSX.Element {
  const [value, setValue] = useState(linkBubble.href);
  const [copied, setCopied] = useState(false);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    setValue(linkBubble.href);
  }, [linkBubble.href]);

  useEffect(() => {
    if (isEditing) {
      requestAnimationFrame(() => {
        inputRef.current?.focus();
        inputRef.current?.select();
      });
    }
  }, [isEditing]);

  return (
    <div
      className="rich-markdown-link-bubble"
      style={{ left: linkBubble.left, top: linkBubble.top }}
      onKeyDown={e => e.stopPropagation()}
    >
      {isEditing ? (
        <>
          <input
            ref={inputRef}
            className="rich-markdown-link-input"
            value={value}
            placeholder="粘贴或输入链接 URL…"
            onChange={e => setValue(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') {
                e.preventDefault();
                onSave(value.trim());
              } else if (e.key === 'Escape') {
                e.preventDefault();
                e.stopPropagation();
                onEditCancel();
              }
            }}
          />
          <button
            type="button"
            className="rich-markdown-link-button"
            title="保存链接"
            onMouseDown={e => e.preventDefault()}
            onClick={() => onSave(value.trim())}
          >
            <Check size={13} />
          </button>
          <button
            type="button"
            className="rich-markdown-link-button"
            title="取消"
            onMouseDown={e => e.preventDefault()}
            onClick={onEditCancel}
          >
            <X size={13} />
          </button>
        </>
      ) : (
        <>
          <span className="rich-markdown-link-url" title={linkBubble.href}>
            {linkBubble.href}
          </span>
          <button
            type="button"
            className="rich-markdown-link-button"
            title="在浏览器中打开"
            onMouseDown={e => e.preventDefault()}
            onClick={() => void openExternal(linkBubble.href)}
          >
            <ExternalLink size={13} />
          </button>
          <button
            type="button"
            className="rich-markdown-link-button"
            title="复制链接"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              void copyText(linkBubble.href).then(() => {
                setCopied(true);
                setTimeout(() => setCopied(false), 1200);
              });
            }}
          >
            {copied ? <Check size={13} /> : <Copy size={13} />}
          </button>
          <button
            type="button"
            className="rich-markdown-link-button"
            title="编辑链接 (⌘K)"
            onMouseDown={e => e.preventDefault()}
            onClick={onEditStart}
          >
            <Pencil size={13} />
          </button>
          <button
            type="button"
            className="rich-markdown-link-button"
            title="移除链接"
            onMouseDown={e => e.preventDefault()}
            onClick={onRemove}
          >
            <Trash2 size={13} />
          </button>
        </>
      )}
    </div>
  );
}
