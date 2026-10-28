import React, { useEffect, useRef, useState } from 'react';
import type { Editor } from '@tiptap/react';
import { Fragment, type Node as PmNode } from '@tiptap/pm/model';
import { TextSelection } from '@tiptap/pm/state';
import { isInTable, moveCellForward, nextCell, selectionCell } from '@tiptap/pm/tables';
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Columns3,
  GripHorizontal,
  GripVertical,
  Plus,
  Rows3,
  Trash2,
} from 'lucide-react';

export const RICH_MARKDOWN_CODE_BLOCK_INDENT = '  ';
const LEADING_INDENT = /^(\t| {1,2})/;

export function handleRichMarkdownTableTab(editor: Editor, shiftKey: boolean): boolean {
  if (!editor.isActive('table')) return false;
  if (shiftKey) {
    editor.commands.goToPreviousCell();
    return true;
  }
  if (editor.commands.goToNextCell()) {
    return true;
  }
  if (editor.can().addRowAfter()) {
    editor.chain().addRowAfter().goToNextCell().run();
  }
  return true;
}

function moveToVerticalNeighbor(editor: Editor, direction: 1 | -1): boolean {
  const { state, view } = editor;
  const $nextCell = nextCell(selectionCell(state), 'vert', direction);
  if (!$nextCell) return false;
  view.dispatch(
    state.tr
      .setSelection(TextSelection.between($nextCell, moveCellForward($nextCell)))
      .scrollIntoView(),
  );
  return true;
}

export function handleRichMarkdownTableEnter(editor: Editor): boolean {
  if (!isInTable(editor.state)) return false;
  if (moveToVerticalNeighbor(editor, 1)) return true;
  if (!editor.can().addRowAfter()) return true;
  editor.commands.addRowAfter();
  moveToVerticalNeighbor(editor, 1);
  return true;
}

function isEmptyCell(cell: PmNode): boolean {
  for (let i = 0; i < cell.childCount; i += 1) {
    const child = cell.child(i);
    if (!child.isTextblock || child.content.size > 0) return false;
  }
  return true;
}

function isHeaderRow(row: PmNode): boolean {
  return row.firstChild?.type.spec.tableRole === 'header_cell';
}

function isEmptyRow(row: PmNode): boolean {
  for (let i = 0; i < row.childCount; i += 1) {
    if (!isEmptyCell(row.child(i))) return false;
  }
  return row.childCount > 0;
}

export function handleRichMarkdownTableBackspace(editor: Editor): boolean {
  const { state } = editor;
  if (!state.selection.empty || !isInTable(state)) return false;
  const $cell = selectionCell(state);
  const cell = $cell.nodeAfter;
  if (!cell || !isEmptyCell(cell) || state.selection.from !== $cell.pos + 2) return false;
  if (!isEmptyRow($cell.parent)) {
    editor.commands.goToPreviousCell();
    return true;
  }
  if ($cell.node(-1).childCount <= 1) {
    return editor.commands.deleteTable();
  }
  if (isHeaderRow($cell.parent)) return true;
  return editor.commands.deleteRow();
}

type ListItemTypeName = 'listItem' | 'taskItem';

function isListItemType(name: string): name is ListItemTypeName {
  return name === 'listItem' || name === 'taskItem';
}

function resolveListItemContext(editor: Editor): {
  itemType: ListItemTypeName;
  itemDepth: number;
  parentItemType: ListItemTypeName | null;
} | null {
  const { $from } = editor.state.selection;
  let found: { itemType: ListItemTypeName; itemDepth: number } | null = null;
  for (let depth = $from.depth; depth > 0; depth--) {
    const name = $from.node(depth).type.name;
    if (!isListItemType(name)) continue;
    if (!found) {
      found = { itemType: name, itemDepth: depth };
      continue;
    }
    return { ...found, parentItemType: name };
  }
  return found ? { ...found, parentItemType: null } : null;
}

export function indentRichMarkdownListItem(editor: Editor): boolean {
  return editor.commands.sinkListItem(resolveListItemContext(editor)?.itemType ?? 'listItem');
}

export function outdentRichMarkdownListItem(editor: Editor): boolean {
  const context = resolveListItemContext(editor);
  if (!context) return false;
  if (context.parentItemType && context.parentItemType !== context.itemType) {
    const { state } = editor;
    const { $from } = state.selection;
    if ($from.index(context.itemDepth - 1) > 0) {
      editor.view.dispatch(state.tr.split($from.before(context.itemDepth), 1));
    }
    const nextCtx = resolveListItemContext(editor);
    if (nextCtx) {
      const s = editor.state;
      const listPos = s.selection.$from.before(nextCtx.itemDepth - 1);
      const list = s.doc.nodeAt(listPos);
      if (list) {
        const itemType = s.schema.nodes[context.parentItemType];
        const listTypeName = context.parentItemType === 'taskItem' ? 'taskList' : 'bulletList';
        const items: PmNode[] = [];
        list.forEach(item => {
          items.push(itemType.create(null, item.content, item.marks));
        });
        const retyped = s.schema.nodes[listTypeName].create(null, Fragment.from(items), list.marks);
        const tr = s.tr.replaceWith(listPos, listPos + list.nodeSize, retyped);
        tr.setSelection(TextSelection.create(tr.doc, s.selection.from, s.selection.to));
        editor.view.dispatch(tr);
      }
    }
    return editor.commands.liftListItem(context.parentItemType);
  }
  return editor.commands.liftListItem(context.itemType);
}

export function outdentRichMarkdownCodeBlock(editor: Editor): boolean {
  const { $from } = editor.state.selection;
  let codeBlock: { text: string; start: number } | null = null;
  for (let depth = $from.depth; depth > 0; depth--) {
    const node = $from.node(depth);
    if (node.type.name === 'codeBlock') {
      codeBlock = { text: node.textContent, start: $from.start(depth) };
      break;
    }
  }
  if (!codeBlock) return false;
  const { from, to } = editor.state.selection;
  const tr = editor.state.tr;
  let lineStart = codeBlock.start;
  for (const line of codeBlock.text.split('\n')) {
    const lineEnd = lineStart + line.length;
    const isTouched = lineEnd >= from && lineStart <= to;
    const indent = isTouched ? LEADING_INDENT.exec(line) : null;
    if (indent) {
      tr.delete(tr.mapping.map(lineStart), tr.mapping.map(lineStart + indent[0].length));
    }
    lineStart = lineEnd + 1;
  }
  if (!tr.docChanged) return false;
  editor.view.dispatch(tr);
  return true;
}

export function RichMarkdownTableControls({
  editor,
  scrollContainerRef,
  disabled = false,
}: {
  editor: Editor | null;
  scrollContainerRef: React.RefObject<HTMLDivElement | null>;
  disabled?: boolean;
}): React.JSX.Element | null {
  const [activeCell, setActiveCell] = useState<{
    cell: HTMLTableCellElement;
    table: HTMLTableElement;
  } | null>(null);
  const [menuAxis, setMenuAxis] = useState<'row' | 'column' | null>(null);
  const [, setTick] = useState(0);
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!editor || disabled) {
      setActiveCell(null);
      return;
    }
    const updateActive = () => {
      if (!isInTable(editor.state)) {
        setActiveCell(null);
        setMenuAxis(null);
        return;
      }
      try {
        const pos = selectionCell(editor.state).pos;
        const dom = editor.view.nodeDOM(pos);
        if (dom instanceof HTMLTableCellElement) {
          const table = dom.closest('table');
          if (table instanceof HTMLTableElement) {
            setActiveCell({ cell: dom, table });
            return;
          }
        }
      } catch {
        // Ignore transient selection states
      }
      setActiveCell(null);
    };
    editor.on('selectionUpdate', updateActive);
    editor.on('update', updateActive);
    updateActive();
    return () => {
      editor.off('selectionUpdate', updateActive);
      editor.off('update', updateActive);
    };
  }, [editor, disabled]);

  useEffect(() => {
    const scroller = scrollContainerRef.current;
    if (!scroller || !activeCell) return;
    const onScroll = () => setTick(v => v + 1);
    scroller.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll);
    return () => {
      scroller.removeEventListener('scroll', onScroll);
      window.removeEventListener('resize', onScroll);
    };
  }, [activeCell, scrollContainerRef]);

  useEffect(() => {
    if (!menuAxis) return;
    const onPointerDown = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setMenuAxis(null);
      }
    };
    window.addEventListener('mousedown', onPointerDown);
    return () => window.removeEventListener('mousedown', onPointerDown);
  }, [menuAxis]);

  const scroller = scrollContainerRef.current;
  if (
    disabled ||
    !editor ||
    !scroller ||
    !activeCell ||
    !activeCell.cell.isConnected ||
    !activeCell.table.isConnected
  ) {
    return null;
  }

  const scrollerRect = scroller.getBoundingClientRect();
  const tableRect = activeCell.table.getBoundingClientRect();
  const cellRect = activeCell.cell.getBoundingClientRect();
  const rowEl = activeCell.cell.parentElement;
  if (!rowEl) return null;
  const rowRect = rowEl.getBoundingClientRect();

  const relTop = (y: number) => y - scrollerRect.top + scroller.scrollTop;
  const relLeft = (x: number) => x - scrollerRect.left + scroller.scrollLeft;

  const isHeaderCell = activeCell.cell.tagName === 'TH';

  return (
    <div ref={containerRef} className="rich-markdown-table-controls">
      <button
        type="button"
        className="rich-markdown-table-grip"
        style={{
          left: Math.max(4, relLeft(tableRect.left) - 18),
          top: relTop((rowRect.top + rowRect.bottom) / 2) - 11,
        }}
        title="行操作"
        onMouseDown={e => e.preventDefault()}
        onClick={() => setMenuAxis(menuAxis === 'row' ? null : 'row')}
      >
        <GripVertical size={12} />
      </button>

      <button
        type="button"
        className="rich-markdown-table-grip"
        style={{
          left: relLeft((cellRect.left + cellRect.right) / 2) - 11,
          top: Math.max(4, relTop(tableRect.top) - 18),
        }}
        title="列操作"
        onMouseDown={e => e.preventDefault()}
        onClick={() => setMenuAxis(menuAxis === 'column' ? null : 'column')}
      >
        <GripHorizontal size={12} />
      </button>

      <button
        type="button"
        className="rich-markdown-table-add-btn"
        style={{
          left: relLeft(tableRect.left),
          top: relTop(tableRect.bottom) + 3,
          width: Math.max(48, tableRect.width),
          height: 18,
        }}
        title="在末尾添加一行"
        onMouseDown={e => e.preventDefault()}
        onClick={() => editor.chain().focus().addRowAfter().run()}
      >
        <Plus size={11} />
      </button>

      <button
        type="button"
        className="rich-markdown-table-add-btn"
        style={{
          left: relLeft(tableRect.right) + 3,
          top: relTop(tableRect.top),
          width: 18,
          height: Math.max(36, tableRect.height),
        }}
        title="在右侧添加一列"
        onMouseDown={e => e.preventDefault()}
        onClick={() => editor.chain().focus().addColumnAfter().run()}
      >
        <Plus size={11} />
      </button>

      {menuAxis === 'row' && (
        <div
          className="rich-markdown-table-menu"
          style={{
            left: Math.max(12, relLeft(tableRect.left)),
            top: relTop(rowRect.bottom) + 4,
          }}
        >
          <button
            type="button"
            disabled={isHeaderCell}
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().addRowBefore().run();
              setMenuAxis(null);
            }}
          >
            <ArrowUp size={13} /> 在上方插入行
          </button>
          <button
            type="button"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().addRowAfter().run();
              setMenuAxis(null);
            }}
          >
            <ArrowDown size={13} /> 在下方插入行
          </button>
          <hr />
          <button
            type="button"
            className="danger"
            disabled={isHeaderCell}
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().deleteRow().run();
              setMenuAxis(null);
            }}
          >
            <Rows3 size={13} /> 删除当前行
          </button>
          <button
            type="button"
            className="danger"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().deleteTable().run();
              setMenuAxis(null);
            }}
          >
            <Trash2 size={13} /> 删除整张表格
          </button>
        </div>
      )}

      {menuAxis === 'column' && (
        <div
          className="rich-markdown-table-menu"
          style={{
            left: Math.max(12, relLeft(cellRect.left)),
            top: relTop(tableRect.top) + 22,
          }}
        >
          <button
            type="button"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().addColumnBefore().run();
              setMenuAxis(null);
            }}
          >
            <ArrowLeft size={13} /> 在左侧插入列
          </button>
          <button
            type="button"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().addColumnAfter().run();
              setMenuAxis(null);
            }}
          >
            <ArrowRight size={13} /> 在右侧插入列
          </button>
          <hr />
          <button
            type="button"
            className="danger"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().deleteColumn().run();
              setMenuAxis(null);
            }}
          >
            <Columns3 size={13} /> 删除当前列
          </button>
          <button
            type="button"
            className="danger"
            onMouseDown={e => e.preventDefault()}
            onClick={() => {
              editor.chain().focus().deleteTable().run();
              setMenuAxis(null);
            }}
          >
            <Trash2 size={13} /> 删除整张表格
          </button>
        </div>
      )}
    </div>
  );
}
