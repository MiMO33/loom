import React from 'react';
import type { Editor } from '@tiptap/react';
import { TextSelection } from '@tiptap/pm/state';
import {
  ChevronRight,
  Code2,
  Heading1,
  Heading2,
  Heading3,
  Heading4,
  Heading5,
  ImageIcon,
  List,
  ListOrdered,
  ListTodo,
  Minus,
  Pilcrow,
  Quote,
  Search,
  Sigma,
  Table2,
  Workflow,
} from 'lucide-react';
import type { ToggleHeadingVariant } from './extensions';

export type SlashMenuState = {
  query: string;
  from: number;
  to: number;
  left: number;
  top: number;
};

export type SlashCommandGroup =
  | '标题'
  | '可折叠标题'
  | '基础块'
  | '高级'
  | '媒体';

export type SlashCommand = {
  id: string;
  label: string;
  aliases: string[];
  icon: React.ComponentType<{ size?: number; className?: string }>;
  group: SlashCommandGroup;
  description: string;
  run: (editor: Editor) => void;
};

export function insertCodeBlock(editor: Editor, language: string, text = ''): void {
  editor.commands.command(({ state, dispatch }) => {
    const codeBlockType = state.schema.nodes.codeBlock;
    if (!codeBlockType) return false;
    const node = codeBlockType.create({ language }, text ? state.schema.text(text) : undefined);
    const tr = state.tr.replaceSelectionWith(node).scrollIntoView();
    const cursor = tr.selection.from + 1;
    tr.setSelection(TextSelection.create(tr.doc, cursor, cursor));
    dispatch?.(tr);
    return true;
  });
}

export function insertToggle(editor: Editor, variant?: ToggleHeadingVariant): void {
  const insertAt = editor.state.selection.from;
  editor
    .chain()
    .focus()
    .insertContentAt(insertAt, {
      type: 'details',
      attrs: {
        open: true,
        ...(variant ? { variant } : {}),
      },
      content: [
        { type: 'detailsSummary' },
        { type: 'detailsContent', content: [{ type: 'paragraph' }] },
      ],
    })
    .setTextSelection(insertAt + 1)
    .run();
}

export const slashCommands: SlashCommand[] = [
  {
    id: 'heading-1',
    label: '一级标题 (H1)',
    aliases: ['h1', 'heading1', 'title'],
    icon: Heading1,
    group: '标题',
    description: '大章节标题',
    run: editor => { editor.chain().focus().setHeading({ level: 1 }).run(); },
  },
  {
    id: 'heading-2',
    label: '二级标题 (H2)',
    aliases: ['h2', 'heading2'],
    icon: Heading2,
    group: '标题',
    description: '中章节标题',
    run: editor => { editor.chain().focus().setHeading({ level: 2 }).run(); },
  },
  {
    id: 'heading-3',
    label: '三级标题 (H3)',
    aliases: ['h3', 'heading3'],
    icon: Heading3,
    group: '标题',
    description: '小章节标题',
    run: editor => { editor.chain().focus().setHeading({ level: 3 }).run(); },
  },
  {
    id: 'heading-4',
    label: '四级标题 (H4)',
    aliases: ['h4', 'heading4'],
    icon: Heading4,
    group: '标题',
    description: '嵌套子标题',
    run: editor => { editor.chain().focus().setHeading({ level: 4 }).run(); },
  },
  {
    id: 'heading-5',
    label: '五级标题 (H5)',
    aliases: ['h5', 'heading5'],
    icon: Heading5,
    group: '标题',
    description: '深层子标题',
    run: editor => { editor.chain().focus().setHeading({ level: 5 }).run(); },
  },
  {
    id: 'toggle-h1',
    label: '折叠一级标题',
    aliases: ['toggle-h1', 'details-h1'],
    icon: ChevronRight,
    group: '可折叠标题',
    description: '带 H1 标题的可折叠区块',
    run: editor => { insertToggle(editor, 'heading-1'); },
  },
  {
    id: 'toggle-h2',
    label: '折叠二级标题',
    aliases: ['toggle-h2', 'details-h2'],
    icon: ChevronRight,
    group: '可折叠标题',
    description: '带 H2 标题的可折叠区块',
    run: editor => { insertToggle(editor, 'heading-2'); },
  },
  {
    id: 'toggle-h3',
    label: '折叠三级标题',
    aliases: ['toggle-h3', 'details-h3'],
    icon: ChevronRight,
    group: '可折叠标题',
    description: '带 H3 标题的可折叠区块',
    run: editor => { insertToggle(editor, 'heading-3'); },
  },
  {
    id: 'text',
    label: '正文段落',
    aliases: ['paragraph', 'plain', 'text', 'p'],
    icon: Pilcrow,
    group: '基础块',
    description: '普通文本段落',
    run: editor => { editor.chain().focus().setParagraph().run(); },
  },
  {
    id: 'bullet-list',
    label: '无序列表',
    aliases: ['bullet', 'ul', 'list'],
    icon: List,
    group: '基础块',
    description: '创建项目符号列表',
    run: editor => { editor.chain().focus().toggleBulletList().run(); },
  },
  {
    id: 'ordered-list',
    label: '有序列表',
    aliases: ['ordered', 'ol', 'numbered'],
    icon: ListOrdered,
    group: '基础块',
    description: '创建编号列表',
    run: editor => { editor.chain().focus().toggleOrderedList().run(); },
  },
  {
    id: 'task-list',
    label: '待办清单 (Checklist)',
    aliases: ['todo', 'task', 'checkbox', 'check'],
    icon: ListTodo,
    group: '基础块',
    description: '创建可勾选任务列表',
    run: editor => { editor.chain().focus().toggleTaskList().run(); },
  },
  {
    id: 'blockquote',
    label: '引用块',
    aliases: ['quote', 'blockquote'],
    icon: Quote,
    group: '基础块',
    description: '插入引用文本',
    run: editor => { editor.chain().focus().toggleBlockquote().run(); },
  },
  {
    id: 'toggle-text',
    label: '折叠区块 (Details)',
    aliases: ['toggle', 'details', 'collapse'],
    icon: ChevronRight,
    group: '基础块',
    description: '创建可展开/收起的区块',
    run: editor => { insertToggle(editor); },
  },
  {
    id: 'code-block',
    label: '代码块',
    aliases: ['code', 'snippet', 'pre'],
    icon: Code2,
    group: '基础块',
    description: '插入带语法高亮的代码块',
    run: editor => { editor.chain().focus().toggleCodeBlock().run(); },
  },
  {
    id: 'divider',
    label: '分割线',
    aliases: ['divider', 'rule', 'hr'],
    icon: Minus,
    group: '基础块',
    description: '插入水平分割线',
    run: editor => { editor.chain().focus().setHorizontalRule().run(); },
  },
  {
    id: 'table',
    label: '表格 (3×3)',
    aliases: ['table', 'grid', 'columns', 'rows'],
    icon: Table2,
    group: '高级',
    description: '插入 3×3 Markdown 表格',
    run: editor => { editor.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(); },
  },
  {
    id: 'mermaid',
    label: 'Mermaid 流程图',
    aliases: ['mermaid', 'diagram', 'flowchart', 'graph'],
    icon: Workflow,
    group: '高级',
    description: '插入 Mermaid 图表代码块',
    run: editor => { insertCodeBlock(editor, 'mermaid', 'graph TD\n  A[开始] --> B[结束]'); },
  },
  {
    id: 'inline-math',
    label: '行内公式 (LaTeX)',
    aliases: ['math', 'latex', 'equation', 'formula'],
    icon: Sigma,
    group: '高级',
    description: '插入行内数学公式',
    run: editor => { editor.commands.insertInlineMath({ latex: 'E = mc^2' }); },
  },
  {
    id: 'math-block',
    label: '块级公式 (LaTeX)',
    aliases: ['math-block', 'display-math', 'equation-block'],
    icon: Sigma,
    group: '高级',
    description: '插入独立块级数学公式',
    run: editor => { editor.commands.insertBlockMath({ latex: '\\sum_{i=1}^{n} x_i' }); },
  },
  {
    id: 'image',
    label: '图片',
    aliases: ['image', 'img', 'picture'],
    icon: ImageIcon,
    group: '媒体',
    description: '从本机选择或上传图片',
    run: editor => { editor.chain().focus().run(); },
  },
];

export function filterSlashCommands(query: string): SlashCommand[] {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return slashCommands;
  return slashCommands.filter(
    cmd =>
      cmd.label.toLowerCase().includes(normalized) ||
      cmd.id.toLowerCase().includes(normalized) ||
      cmd.aliases.some(alias => alias.toLowerCase().includes(normalized)),
  );
}

export function runSlashCommand(
  editor: Editor,
  slashMenu: { from: number; to: number },
  command: SlashCommand,
  onImageCommand?: () => void,
): void {
  editor.chain().focus().deleteRange({ from: slashMenu.from, to: slashMenu.to }).run();
  if (command.id === 'image' && onImageCommand) {
    onImageCommand();
    return;
  }
  command.run(editor);
}

export function syncSlashMenu(
  editor: Editor,
  root: HTMLDivElement | null,
  setSlashMenu: React.Dispatch<React.SetStateAction<SlashMenuState | null>>,
): void {
  if (!root || editor.view.composing || !editor.isEditable) {
    setSlashMenu(null);
    return;
  }
  const { state, view } = editor;
  const { selection } = state;
  if (!selection.empty) {
    setSlashMenu(null);
    return;
  }
  const { $from } = selection;
  if (!$from.parent.isTextblock || $from.parent.type.name === 'codeBlock') {
    setSlashMenu(null);
    return;
  }
  const blockTextBeforeCursor = $from.parent.textBetween(0, $from.parentOffset, '\0', '\0');
  const slashMatch = blockTextBeforeCursor.match(/^\s*\/([a-z0-9-]*)$/i);
  if (!slashMatch) {
    setSlashMenu(null);
    return;
  }
  const slashOffset = blockTextBeforeCursor.lastIndexOf('/');
  const start = selection.from - ($from.parentOffset - slashOffset);
  const coords = view.coordsAtPos(selection.from);
  const rect = root.getBoundingClientRect();
  setSlashMenu({
    query: slashMatch[1] ?? '',
    from: start,
    to: selection.from,
    left: Math.max(8, coords.left - rect.left),
    top: coords.bottom - rect.top + 8,
  });
}

export function RichMarkdownSlashMenu({
  editor,
  slashMenu,
  filteredCommands,
  selectedIndex,
  onImagePick,
}: {
  editor: Editor | null;
  slashMenu: SlashMenuState;
  filteredCommands: SlashCommand[];
  selectedIndex: number;
  onImagePick: () => void;
}): React.JSX.Element {
  let currentGroup: SlashCommand['group'] | null = null;

  return (
    <div
      className="rich-markdown-slash-menu"
      style={{ left: slashMenu.left, top: slashMenu.top }}
      role="dialog"
      aria-label="插入块命令"
    >
      <div className="rich-markdown-slash-search" onMouseDown={e => e.preventDefault()}>
        <Search size={13} />
        <input
          aria-label="搜索块"
          readOnly
          type="text"
          value={slashMenu.query}
          placeholder="筛选要插入的块…"
        />
      </div>
      <div className="rich-markdown-slash-results" role="listbox">
        {filteredCommands.length === 0 ? (
          <div className="rich-markdown-slash-empty">无匹配的块命令</div>
        ) : (
          filteredCommands.map((command, index) => {
            const showGroup = command.group !== currentGroup;
            currentGroup = command.group;
            const Icon = command.icon;
            return (
              <React.Fragment key={command.id}>
                {showGroup ? (
                  <div className="rich-markdown-slash-section">{command.group}</div>
                ) : null}
                <button
                  type="button"
                  title={command.description}
                  role="option"
                  aria-selected={index === selectedIndex}
                  className={`rich-markdown-slash-item ${index === selectedIndex ? 'is-active' : ''}`}
                  onMouseDown={e => e.preventDefault()}
                  onClick={() => editor && runSlashCommand(editor, slashMenu, command, onImagePick)}
                >
                  <span className="rich-markdown-slash-icon">
                    <Icon size={14} />
                  </span>
                  <span className="rich-markdown-slash-item-body">
                    <span className="rich-markdown-slash-label">{command.label}</span>
                    <small>{command.description}</small>
                  </span>
                </button>
              </React.Fragment>
            );
          })
        )}
      </div>
    </div>
  );
}
