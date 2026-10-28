import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { EditorContent, useEditor, type Editor } from '@tiptap/react';
import {
  Bold,
  Check,
  ChevronRight,
  Clipboard,
  Code,
  Code2,
  ExternalLink,
  Eye,
  FileCode2,
  FileText,
  Heading1,
  Heading2,
  Heading3,
  Heading4,
  Heading5,
  ImageIcon,
  Italic,
  Link as LinkIcon,
  List,
  ListOrdered,
  ListTodo,
  ListTree,
  LoaderCircle,
  Minus,
  MoreHorizontal,
  Pilcrow,
  Quote,
  Save,
  Search,
  Sigma,
  Sparkles,
  Strikethrough,
  Table2,
  Underline,
  Workflow,
  X,
} from 'lucide-react';
import { api, copyText, post } from '../api';
import { Markdown } from '../components';
import { createRichMarkdownExtensions } from './extensions';
import {
  extractFrontMatter,
  extractTableOfContents,
  markdownFrontMatterInner,
  prependFrontMatter,
} from './frontmatter';
import { getLinkBubblePosition, RichMarkdownLinkBubble, type LinkBubbleState } from './link-bubble';
import { RichMarkdownSearchBar, useRichMarkdownSearch } from './search';
import {
  filterSlashCommands,
  insertCodeBlock,
  insertToggle,
  RichMarkdownSlashMenu,
  runSlashCommand,
  syncSlashMenu,
  type SlashMenuState,
} from './slash-commands';
import {
  handleRichMarkdownTableBackspace,
  handleRichMarkdownTableEnter,
  handleRichMarkdownTableTab,
  indentRichMarkdownListItem,
  outdentRichMarkdownCodeBlock,
  outdentRichMarkdownListItem,
  RICH_MARKDOWN_CODE_BLOCK_INDENT,
  RichMarkdownTableControls,
} from './table-controls';

export type MarkdownEditorMode = 'rich' | 'source' | 'preview';

export interface OrcaMarkdownEditorProps {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  disabled?: boolean;
  compact?: boolean;
  defaultMode?: MarkdownEditorMode;
  onSave?: (currentValue: string) => void;
  onPasteOrDropFiles?: (files: File[]) => void;
}

export function OrcaMarkdownEditor({
  value,
  onChange,
  placeholder = '输入 Markdown 内容，或输入 / 插入块…',
  disabled = false,
  compact = false,
  defaultMode = 'rich',
  onSave,
  onPasteOrDropFiles,
}: OrcaMarkdownEditorProps): React.JSX.Element {
  const [mode, setMode] = useState<MarkdownEditorMode>(defaultMode);
  const [showToc, setShowToc] = useState(false);
  const [moreMenuOpen, setMoreMenuOpen] = useState(false);
  const [slashMenu, setSlashMenu] = useState<SlashMenuState | null>(null);
  const [selectedSlashIndex, setSelectedSlashIndex] = useState(0);
  const [linkBubble, setLinkBubble] = useState<LinkBubbleState | null>(null);
  const [isEditingLink, setIsEditingLink] = useState(false);

  const rootRef = useRef<HTMLDivElement | null>(null);
  const scrollContainerRef = useRef<HTMLDivElement | null>(null);
  const moreMenuRef = useRef<HTMLDivElement | null>(null);
  const imageInputRef = useRef<HTMLInputElement | null>(null);
  const editorRef = useRef<Editor | null>(null);
  const applyingExternalRef = useRef(false);

  const frontMatter = useMemo(() => extractFrontMatter(value), [value]);
  const bodyValue = frontMatter ? frontMatter.body : value;
  const frontMatterRawRef = useRef(frontMatter?.raw ?? null);
  frontMatterRawRef.current = frontMatter?.raw ?? null;

  const lastSyncedBodyRef = useRef(bodyValue);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const onSaveRef = useRef(onSave);
  onSaveRef.current = onSave;
  const onPasteOrDropFilesRef = useRef(onPasteOrDropFiles);
  onPasteOrDropFilesRef.current = onPasteOrDropFiles;

  const slashMenuRef = useRef<SlashMenuState | null>(null);
  slashMenuRef.current = slashMenu;
  const filteredSlashCommands = useMemo(
    () => (slashMenu ? filterSlashCommands(slashMenu.query) : []),
    [slashMenu],
  );
  const filteredSlashCommandsRef = useRef(filteredSlashCommands);
  filteredSlashCommandsRef.current = filteredSlashCommands;
  const selectedSlashIndexRef = useRef(selectedSlashIndex);
  selectedSlashIndexRef.current = selectedSlashIndex;
  const isEditingLinkRef = useRef(isEditingLink);
  isEditingLinkRef.current = isEditingLink;

  useEffect(() => {
    setSelectedSlashIndex(0);
  }, [slashMenu?.query]);

  useEffect(() => {
    if (!moreMenuOpen) return;
    const onPointerDown = (e: MouseEvent) => {
      if (moreMenuRef.current && !moreMenuRef.current.contains(e.target as Node)) {
        setMoreMenuOpen(false);
      }
    };
    window.addEventListener('mousedown', onPointerDown);
    return () => window.removeEventListener('mousedown', onPointerDown);
  }, [moreMenuOpen]);

  const emitBodyChange = useCallback((nextBody: string) => {
    lastSyncedBodyRef.current = nextBody;
    const full = frontMatterRawRef.current
      ? prependFrontMatter(frontMatterRawRef.current, nextBody)
      : nextBody;
    onChangeRef.current(full);
  }, []);

  const openLinkEditor = useCallback(() => {
    const ed = editorRef.current;
    if (!ed || !ed.isEditable) return;
    const pos = getLinkBubblePosition(ed, rootRef.current);
    if (!pos) {
      ed.commands.focus();
      return;
    }
    const href = ed.isActive('link') ? String(ed.getAttributes('link').href ?? '') : '';
    setLinkBubble({ href, left: pos.left, top: pos.top });
    setIsEditingLink(true);
  }, []);

  const triggerImagePick = useCallback(() => {
    imageInputRef.current?.click();
  }, []);

  const insertImageFiles = useCallback((files: File[]) => {
    const images = files.filter(f => f.type.startsWith('image/'));
    if (!images.length) return false;
    if (onPasteOrDropFilesRef.current) {
      onPasteOrDropFilesRef.current(images);
      return true;
    }
    images.forEach(file => {
      const reader = new FileReader();
      reader.onload = () => {
        if (typeof reader.result === 'string') {
          editorRef.current?.chain().focus().setImage({ src: reader.result, alt: file.name }).run();
        }
      };
      reader.readAsDataURL(file);
    });
    return true;
  }, []);

  const extensions = useMemo(() => createRichMarkdownExtensions(placeholder), [placeholder]);

  const editor = useEditor(
    {
      immediatelyRender: false,
      extensions,
      editable: !disabled,
      content: bodyValue,
      contentType: 'markdown',
      editorProps: {
        attributes: {
          class: `rich-markdown-editor ${compact ? 'is-compact' : ''}`,
          spellcheck: 'true',
        },
        handlePaste: (_view, event) => {
          const files = Array.from(event.clipboardData?.files ?? []);
          if (files.some(f => f.type.startsWith('image/'))) {
            event.preventDefault();
            return insertImageFiles(files);
          }
          return false;
        },
        handleDrop: (_view, event) => {
          const files = Array.from(event.dataTransfer?.files ?? []);
          if (files.length > 0) {
            event.preventDefault();
            if (onPasteOrDropFilesRef.current) {
              onPasteOrDropFilesRef.current(files);
              return true;
            }
            return insertImageFiles(files);
          }
          return false;
        },
        handleKeyDown: (_view, event) => {
          if (event.isComposing) return false;
          const ed = editorRef.current;
          if (!ed) return false;
          const mod = event.metaKey || event.ctrlKey;

          if (mod && event.key.toLowerCase() === 's') {
            if (onSaveRef.current) {
              event.preventDefault();
              const md = ed.getMarkdown();
              const full = frontMatterRawRef.current
                ? prependFrontMatter(frontMatterRawRef.current, md)
                : md;
              onSaveRef.current(full);
              return true;
            }
          }

          if (mod && event.key.toLowerCase() === 'k') {
            event.preventDefault();
            openLinkEditor();
            return true;
          }

          if (mod && event.shiftKey && event.key.toLowerCase() === 'x') {
            event.preventDefault();
            ed.chain().focus().toggleStrike().run();
            return true;
          }

          // Slash menu navigation
          if (slashMenuRef.current) {
            const cmds = filteredSlashCommandsRef.current;
            if (event.key === 'ArrowDown') {
              event.preventDefault();
              if (cmds.length > 0) {
                setSelectedSlashIndex(idx => (idx + 1) % cmds.length);
              }
              return true;
            }
            if (event.key === 'ArrowUp') {
              event.preventDefault();
              if (cmds.length > 0) {
                setSelectedSlashIndex(idx => (idx - 1 + cmds.length) % cmds.length);
              }
              return true;
            }
            if (event.key === 'Enter' || event.key === 'Tab') {
              const chosen = cmds[selectedSlashIndexRef.current] ?? cmds[0];
              if (chosen) {
                event.preventDefault();
                const currentMenu = slashMenuRef.current;
                setSlashMenu(null);
                runSlashCommand(ed, currentMenu, chosen, triggerImagePick);
                return true;
              }
            }
            if (event.key === 'Escape') {
              event.preventDefault();
              setSlashMenu(null);
              return true;
            }
          }

          if (event.key === 'Backspace' && handleRichMarkdownTableBackspace(ed)) {
            event.preventDefault();
            return true;
          }

          if (event.key === 'Enter' && handleRichMarkdownTableEnter(ed)) {
            event.preventDefault();
            return true;
          }

          if (event.key === 'Tab') {
            event.preventDefault();
            if (handleRichMarkdownTableTab(ed, event.shiftKey)) {
              return true;
            }
            if (event.shiftKey) {
              if (!outdentRichMarkdownListItem(ed)) {
                outdentRichMarkdownCodeBlock(ed);
              }
              return true;
            }
            if (indentRichMarkdownListItem(ed)) {
              return true;
            }
            if (ed.isActive('codeBlock')) {
              ed.commands.insertContent(RICH_MARKDOWN_CODE_BLOCK_INDENT);
            }
            return true;
          }

          return false;
        },
      },
      onCreate: ({ editor: nextEditor }) => {
        editorRef.current = nextEditor;
        lastSyncedBodyRef.current = bodyValue;
      },
      onDestroy: () => {
        editorRef.current = null;
      },
      onUpdate: ({ editor: nextEditor }) => {
        if (applyingExternalRef.current) return;
        const md = nextEditor.getMarkdown();
        emitBodyChange(md);
        syncSlashMenu(nextEditor, rootRef.current, setSlashMenu);
      },
      onSelectionUpdate: ({ editor: nextEditor }) => {
        syncSlashMenu(nextEditor, rootRef.current, setSlashMenu);
        if (isEditingLinkRef.current) return;
        if (nextEditor.isActive('link')) {
          const pos = getLinkBubblePosition(nextEditor, rootRef.current);
          if (pos) {
            setLinkBubble({
              href: String(nextEditor.getAttributes('link').href ?? ''),
              left: pos.left,
              top: pos.top,
            });
            return;
          }
        }
        setLinkBubble(null);
      },
    },
    [],
  );

  const search = useRichMarkdownSearch(editor);

  useEffect(() => {
    editorRef.current = editor ?? null;
  }, [editor]);

  useEffect(() => {
    editor?.setEditable(!disabled);
  }, [editor, disabled]);

  useEffect(() => {
    if (!editor || bodyValue === lastSyncedBodyRef.current) return;
    const currentMd = editor.getMarkdown();
    if (currentMd === bodyValue) {
      lastSyncedBodyRef.current = bodyValue;
      return;
    }
    applyingExternalRef.current = true;
    try {
      editor.commands.setContent(bodyValue, {
        contentType: 'markdown',
        emitUpdate: false,
      });
      lastSyncedBodyRef.current = bodyValue;
    } finally {
      applyingExternalRef.current = false;
    }
  }, [editor, bodyValue]);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!rootRef.current || !rootRef.current.contains(e.target as Node)) return;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === 'f') {
        if (mode === 'rich') {
          e.preventDefault();
          e.stopPropagation();
          search.openSearch(e.altKey);
        }
      }
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [mode, search]);

  const tocItems = useMemo(() => extractTableOfContents(bodyValue), [bodyValue]);

  const scrollToHeading = useCallback(
    (headingText: string, level: number) => {
      const container = scrollContainerRef.current;
      if (!container) return;
      const headings = container.querySelectorAll(`h${level}`);
      for (const h of Array.from(headings)) {
        if (h.textContent?.trim() === headingText) {
          h.scrollIntoView({ behavior: 'smooth', block: 'start' });
          return;
        }
      }
    },
    [],
  );

  return (
    <div ref={rootRef} className={`orca-markdown-editor-root ${compact ? 'is-compact' : ''}`}>
      <div className="rich-markdown-editor-toolbar">
        {mode === 'rich' ? (
          <div className="rich-markdown-toolbar-group">
            <button
              type="button"
              className="rich-markdown-toolbar-button"
              title="正文段落"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().setParagraph().run()}
            >
              <Pilcrow size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('heading', { level: 1 }) ? 'is-active' : ''}`}
              title="一级标题 (H1)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleHeading({ level: 1 }).run()}
            >
              <Heading1 size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('heading', { level: 2 }) ? 'is-active' : ''}`}
              title="二级标题 (H2)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleHeading({ level: 2 }).run()}
            >
              <Heading2 size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('heading', { level: 3 }) ? 'is-active' : ''}`}
              title="三级标题 (H3)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleHeading({ level: 3 }).run()}
            >
              <Heading3 size={14} />
            </button>
            <div className="rich-markdown-toolbar-separator" />
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('bold') ? 'is-active' : ''}`}
              title="粗体 (⌘B)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleBold().run()}
            >
              <Bold size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('italic') ? 'is-active' : ''}`}
              title="斜体 (⌘I)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleItalic().run()}
            >
              <Italic size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('underline') ? 'is-active' : ''}`}
              title="下划线 (⌘U)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleUnderline().run()}
            >
              <Underline size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('strike') ? 'is-active' : ''}`}
              title="删除线 (⌘⇧X)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleStrike().run()}
            >
              <Strikethrough size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('code') ? 'is-active' : ''}`}
              title="行内代码 (⌘E)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleCode().run()}
            >
              <Code size={14} />
            </button>
            <div className="rich-markdown-toolbar-separator" />
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('bulletList') ? 'is-active' : ''}`}
              title="无序列表"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleBulletList().run()}
            >
              <List size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('orderedList') ? 'is-active' : ''}`}
              title="有序列表"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleOrderedList().run()}
            >
              <ListOrdered size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('taskList') ? 'is-active' : ''}`}
              title="待办清单 (Checklist)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleTaskList().run()}
            >
              <ListTodo size={14} />
            </button>
            <div className="rich-markdown-toolbar-separator" />
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('blockquote') ? 'is-active' : ''}`}
              title="引用块"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleBlockquote().run()}
            >
              <Quote size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('codeBlock') ? 'is-active' : ''}`}
              title="代码块"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() => editor?.chain().focus().toggleCodeBlock().run()}
            >
              <Code2 size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('table') ? 'is-active' : ''}`}
              title="插入 3×3 表格"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={() =>
                editor?.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run()
              }
            >
              <Table2 size={14} />
            </button>
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${editor?.isActive('link') ? 'is-active' : ''}`}
              title="插入或编辑链接 (⌘K)"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={openLinkEditor}
            >
              <LinkIcon size={14} />
            </button>
            <button
              type="button"
              className="rich-markdown-toolbar-button"
              title="插入图片"
              disabled={disabled}
              onMouseDown={e => e.preventDefault()}
              onClick={triggerImagePick}
            >
              <ImageIcon size={14} />
            </button>
            <div ref={moreMenuRef} className="rich-markdown-more-wrap">
              <button
                type="button"
                className={`rich-markdown-toolbar-button ${moreMenuOpen ? 'is-active' : ''}`}
                title="更多块 (折叠区块、公式、H4/H5)"
                disabled={disabled}
                onMouseDown={e => e.preventDefault()}
                onClick={() => setMoreMenuOpen(v => !v)}
              >
                <MoreHorizontal size={14} />
              </button>
              {moreMenuOpen && (
                <div className="rich-markdown-more-menu">
                  <div className="rich-markdown-more-label">标题与折叠</div>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      editor?.chain().focus().toggleHeading({ level: 4 }).run();
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Heading4 size={13} /> 四级标题 (H4)
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      editor?.chain().focus().toggleHeading({ level: 5 }).run();
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Heading5 size={13} /> 五级标题 (H5)
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      if (editor) insertToggle(editor);
                      setMoreMenuOpen(false);
                    }}
                  >
                    <ChevronRight size={13} /> 可折叠区块 (Details)
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      if (editor) insertToggle(editor, 'heading-1');
                      setMoreMenuOpen(false);
                    }}
                  >
                    <ChevronRight size={13} /> 折叠一级标题
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      if (editor) insertToggle(editor, 'heading-2');
                      setMoreMenuOpen(false);
                    }}
                  >
                    <ChevronRight size={13} /> 折叠二级标题
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      if (editor) insertToggle(editor, 'heading-3');
                      setMoreMenuOpen(false);
                    }}
                  >
                    <ChevronRight size={13} /> 折叠三级标题
                  </button>
                  <hr />
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      editor?.chain().focus().setHorizontalRule().run();
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Minus size={13} /> 水平分割线
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      if (editor) insertCodeBlock(editor, 'mermaid', 'graph TD\n  A[开始] --> B[结束]');
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Workflow size={13} /> Mermaid 流程图
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      editor?.commands.insertInlineMath({ latex: 'E = mc^2' });
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Sigma size={13} /> 行内数学公式 (LaTeX)
                  </button>
                  <button
                    type="button"
                    onMouseDown={e => e.preventDefault()}
                    onClick={() => {
                      editor?.commands.insertBlockMath({ latex: '\\sum_{i=1}^{n} x_i' });
                      setMoreMenuOpen(false);
                    }}
                  >
                    <Sigma size={13} /> 数学公式块 (LaTeX)
                  </button>
                </div>
              )}
            </div>
          </div>
        ) : (
          <div className="rich-markdown-toolbar-mode-label">
            {mode === 'source' ? (
              <>
                <FileCode2 size={13} /> Markdown 源码模式
              </>
            ) : (
              <>
                <Eye size={13} /> 预览模式
              </>
            )}
          </div>
        )}

        <div className="rich-markdown-toolbar-right">
          {mode === 'rich' && (
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${search.isOpen ? 'is-active' : ''}`}
              title="查找与替换 (⌘F)"
              onMouseDown={e => e.preventDefault()}
              onClick={() => (search.isOpen ? search.closeSearch() : search.openSearch())}
            >
              <Search size={13} />
            </button>
          )}
          {!compact && tocItems.length > 0 && (
            <button
              type="button"
              className={`rich-markdown-toolbar-button ${showToc ? 'is-active' : ''}`}
              title="目录大纲 (Table of Contents)"
              onMouseDown={e => e.preventDefault()}
              onClick={() => setShowToc(v => !v)}
            >
              <ListTree size={13} />
            </button>
          )}
          <div className="rich-markdown-mode-segmented" role="tablist" aria-label="编辑器视图模式">
            <button
              type="button"
              role="tab"
              aria-selected={mode === 'rich'}
              className={mode === 'rich' ? 'is-active' : ''}
              onClick={() => setMode('rich')}
              title="Orca 所见即所得富文本模式"
            >
              <Sparkles size={11} /> 富文本
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={mode === 'source'}
              className={mode === 'source' ? 'is-active' : ''}
              onClick={() => setMode('source')}
              title="Markdown 原始源码模式"
            >
              <FileCode2 size={11} /> 源码
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={mode === 'preview'}
              className={mode === 'preview' ? 'is-active' : ''}
              onClick={() => setMode('preview')}
              title="只读渲染预览"
            >
              <Eye size={11} /> 预览
            </button>
          </div>
        </div>
      </div>

      <input
        ref={imageInputRef}
        type="file"
        accept="image/*"
        multiple
        hidden
        onChange={e => {
          const files = Array.from(e.target.files ?? []);
          if (files.length) insertImageFiles(files);
          e.target.value = '';
        }}
      />

      {frontMatter && mode !== 'source' && (
        <div className="orca-frontmatter-banner">
          <div className="orca-frontmatter-title">
            <span>FRONT MATTER</span>
            <button type="button" className="text-button" onClick={() => setMode('source')}>
              在源码模式中编辑
            </button>
          </div>
          <pre>{markdownFrontMatterInner(frontMatter)}</pre>
        </div>
      )}

      <div className="rich-markdown-editor-layout">
        {showToc && tocItems.length > 0 && (
          <aside className="markdown-toc-panel" aria-label="文档目录">
            <div className="markdown-toc-header">
              <span>目录大纲</span>
              <button type="button" className="icon-button" onClick={() => setShowToc(false)}>
                <X size={12} />
              </button>
            </div>
            <div className="markdown-toc-list">
              {tocItems.map(item => (
                <button
                  key={item.id}
                  type="button"
                  className="markdown-toc-row"
                  style={{ paddingLeft: `${8 + (item.level - 1) * 12}px` }}
                  onClick={() => scrollToHeading(item.text, item.level)}
                >
                  {item.text}
                </button>
              ))}
            </div>
          </aside>
        )}

        <div ref={scrollContainerRef} className="rich-markdown-editor-shell">
          {mode === 'rich' && (
            <>
              <RichMarkdownSearchBar search={search} />
              <div
                className="rich-markdown-editor-canvas"
                onClick={e => {
                  if (
                    editor &&
                    editor.isEmpty &&
                    e.target === e.currentTarget
                  ) {
                    editor.commands.focus();
                  }
                }}
              >
                <EditorContent editor={editor} />
                <RichMarkdownTableControls
                  editor={editor}
                  scrollContainerRef={scrollContainerRef}
                  disabled={disabled}
                />
              </div>
              {slashMenu && (
                <RichMarkdownSlashMenu
                  editor={editor}
                  slashMenu={slashMenu}
                  filteredCommands={filteredSlashCommands}
                  selectedIndex={selectedSlashIndex}
                  onImagePick={triggerImagePick}
                />
              )}
              {linkBubble && (
                <RichMarkdownLinkBubble
                  linkBubble={linkBubble}
                  isEditing={isEditingLink}
                  onSave={href => {
                    if (!editor) return;
                    if (!href) {
                      editor.chain().focus().extendMarkRange('link').unsetLink().run();
                      setLinkBubble(null);
                      setIsEditingLink(false);
                      return;
                    }
                    if (editor.state.selection.empty && !editor.isActive('link')) {
                      editor
                        .chain()
                        .focus()
                        .insertContent({
                          type: 'text',
                          text: href,
                          marks: [{ type: 'link', attrs: { href } }],
                        })
                        .run();
                    } else {
                      editor.chain().focus().extendMarkRange('link').setLink({ href }).run();
                    }
                    setLinkBubble(prev => (prev ? { ...prev, href } : null));
                    setIsEditingLink(false);
                  }}
                  onRemove={() => {
                    editor?.chain().focus().extendMarkRange('link').unsetLink().run();
                    setLinkBubble(null);
                    setIsEditingLink(false);
                  }}
                  onEditStart={() => setIsEditingLink(true)}
                  onEditCancel={() => {
                    setIsEditingLink(false);
                    if (!editor?.isActive('link')) setLinkBubble(null);
                  }}
                />
              )}
            </>
          )}

          {mode === 'source' && (
            <textarea
              className="orca-markdown-source-textarea"
              value={value}
              disabled={disabled}
              placeholder={placeholder}
              onChange={e => onChange(e.target.value)}
              onKeyDown={e => {
                if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's' && onSave) {
                  e.preventDefault();
                  onSave(value);
                  return;
                }
                if (e.key === 'Tab') {
                  e.preventDefault();
                  const target = e.currentTarget;
                  const start = target.selectionStart;
                  const end = target.selectionEnd;
                  const next = value.slice(0, start) + '  ' + value.slice(end);
                  onChange(next);
                  requestAnimationFrame(() => {
                    target.selectionStart = target.selectionEnd = start + 2;
                  });
                }
              }}
              onPaste={e => {
                const files = Array.from(e.clipboardData.files).filter(f => f.type.startsWith('image/'));
                if (files.length && onPasteOrDropFiles) {
                  e.preventDefault();
                  onPasteOrDropFiles(files);
                }
              }}
            />
          )}

          {mode === 'preview' && (
            <div className="orca-markdown-preview-pane">
              <Markdown text={bodyValue || '*暂无内容*'} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

export interface MarkdownDocumentTarget {
  taskId: string;
  fileId?: string;
  archive?: boolean;
  title?: string;
}

export function MarkdownDocumentModal({
  target,
  onClose,
  notify,
  onSaved,
}: {
  target: MarkdownDocumentTarget;
  onClose: () => void;
  notify: (msg: string) => void;
  onSaved?: () => void;
}): React.JSX.Element {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [filePath, setFilePath] = useState('');
  const [fileName, setFileName] = useState(target.title || 'Markdown 文档');
  const [initialContent, setInitialContent] = useState('');
  const [content, setContent] = useState('');

  const dirty = content !== initialContent;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    const qs = target.archive ? '?archive=1' : `?fileId=${encodeURIComponent(target.fileId || '')}`;
    void api<{ path: string; name: string; content: string }>(`/tasks/${target.taskId}/markdown${qs}`)
      .then(data => {
        if (cancelled) return;
        setFilePath(data.path);
        setFileName(data.name);
        setInitialContent(data.content);
        setContent(data.content);
      })
      .catch(err => {
        if (!cancelled) setError((err as Error).message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [target.taskId, target.fileId, target.archive]);

  const saveDocument = useCallback(
    async (textToSave = content) => {
      setSaving(true);
      setError('');
      try {
        await api(`/tasks/${target.taskId}/markdown`, {
          method: 'PUT',
          body: JSON.stringify({
            fileId: target.fileId,
            archive: !!target.archive,
            content: textToSave,
          }),
        });
        setInitialContent(textToSave);
        notify('Markdown 文档已保存');
        onSaved?.();
      } catch (err) {
        setError((err as Error).message);
      } finally {
        setSaving(false);
      }
    },
    [content, target.taskId, target.fileId, target.archive, notify, onSaved],
  );

  return (
    <div
      className="orca-doc-modal-backdrop"
      onClick={() => {
        if (!dirty || window.confirm('文档有未保存的修改，确定关闭吗？')) {
          onClose();
        }
      }}
    >
      <section
        className="orca-doc-modal"
        role="dialog"
        aria-modal="true"
        aria-label={`编辑 ${fileName}`}
        onClick={e => e.stopPropagation()}
      >
        <header className="orca-doc-modal-header">
          <div className="orca-doc-modal-title">
            <FileText size={15} />
            <strong>{fileName}</strong>
            {dirty && <span className="orca-dirty-dot" title="有未保存修改">● 未保存</span>}
            {filePath && (
              <code className="orca-doc-path" title={filePath}>
                {filePath}
              </code>
            )}
          </div>
          <div className="orca-doc-modal-actions">
            {filePath && (
              <>
                <button
                  type="button"
                  className="icon-button"
                  title="复制文件路径"
                  onClick={() => void copyText(filePath).then(() => notify('文件路径已复制'))}
                >
                  <Clipboard size={14} />
                </button>
                <button
                  type="button"
                  className="icon-button"
                  title="在外部应用中打开"
                  onClick={() =>
                    void post(`/tasks/${target.taskId}/open`, {
                      fileId: target.fileId,
                      archive: target.archive,
                    })
                  }
                >
                  <ExternalLink size={14} />
                </button>
              </>
            )}
            <button
              type="button"
              className="button primary small"
              disabled={saving || loading || !dirty}
              onClick={() => void saveDocument(content)}
              title="保存修改 (⌘S)"
            >
              {saving ? <LoaderCircle size={13} className="spin" /> : dirty ? <Save size={13} /> : <Check size={13} />}
              {saving ? '保存中…' : dirty ? '保存 (⌘S)' : '已保存'}
            </button>
            <button
              type="button"
              className="icon-button"
              aria-label="关闭编辑器"
              onClick={() => {
                if (!dirty || window.confirm('文档有未保存的修改，确定关闭吗？')) {
                  onClose();
                }
              }}
            >
              <X size={16} />
            </button>
          </div>
        </header>

        {error && (
          <div role="alert" className="inline-error" style={{ margin: '10px 18px 0' }}>
            {error}
          </div>
        )}

        <div className="orca-doc-modal-body">
          {loading ? (
            <div className="loading-line" style={{ padding: '40px 24px' }}>
              <LoaderCircle size={16} className="spin" /> 正在读取 Markdown 文档…
            </div>
          ) : (
            <OrcaMarkdownEditor
              value={content}
              onChange={setContent}
              onSave={text => void saveDocument(text)}
              placeholder="输入 Markdown 内容，或输入 / 插入块…"
            />
          )}
        </div>
      </section>
    </div>
  );
}
