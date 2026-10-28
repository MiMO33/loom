import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Editor } from '@tiptap/react';
import type { Node as ProseMirrorNode } from '@tiptap/pm/model';
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import {
  CaseSensitive,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Replace,
  ReplaceAll,
  WholeWord,
  X,
} from 'lucide-react';

export type SearchMatch = { from: number; to: number };

type SearchPluginState = {
  activeIndex: number;
  decorations: DecorationSet;
  query: string;
};

type SearchPluginMeta = {
  activeIndex: number;
  matches: SearchMatch[];
  query: string;
};

export const richMarkdownSearchPluginKey = new PluginKey<SearchPluginState>('richMarkdownSearch');

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function findRichMarkdownSearchMatches(
  doc: ProseMirrorNode,
  query: string,
  options: { matchCase: boolean; wholeWord: boolean },
): SearchMatch[] {
  if (!query || query.length > 512) return [];
  const escaped = escapeRegExp(query);
  const pattern = options.wholeWord ? `\\b${escaped}\\b` : escaped;
  const flags = options.matchCase ? 'g' : 'gi';
  let regex: RegExp;
  try {
    regex = new RegExp(pattern, flags);
  } catch {
    return [];
  }

  const matches: SearchMatch[] = [];
  doc.descendants((node, pos) => {
    if (!node.isText || !node.text) return true;
    regex.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = regex.exec(node.text)) !== null) {
      if (m[0].length === 0) {
        regex.lastIndex += 1;
        continue;
      }
      matches.push({ from: pos + m.index, to: pos + m.index + m[0].length });
    }
    return true;
  });
  return matches;
}

export function createRichMarkdownSearchPlugin(): Plugin<SearchPluginState> {
  return new Plugin<SearchPluginState>({
    key: richMarkdownSearchPluginKey,
    state: {
      init: () => ({
        activeIndex: -1,
        decorations: DecorationSet.empty,
        query: '',
      }),
      apply: (tr, pluginState) => {
        const meta = tr.getMeta(richMarkdownSearchPluginKey) as SearchPluginMeta | undefined;
        const query = meta ? meta.query : pluginState.query;
        const activeIndex = meta ? meta.activeIndex : pluginState.activeIndex;
        if (!query) {
          return { activeIndex: -1, decorations: DecorationSet.empty, query: '' };
        }
        if (meta) {
          const decs = meta.matches.map((match, idx) =>
            Decoration.inline(match.from, match.to, {
              class: 'rich-markdown-search-match',
              'data-active': idx === activeIndex ? 'true' : undefined,
            }),
          );
          return {
            activeIndex,
            decorations: DecorationSet.create(tr.doc, decs),
            query,
          };
        }
        if (tr.docChanged) {
          return {
            activeIndex: pluginState.activeIndex,
            decorations: pluginState.decorations.map(tr.mapping, tr.doc),
            query: pluginState.query,
          };
        }
        return pluginState;
      },
    },
    props: {
      decorations(state) {
        return richMarkdownSearchPluginKey.getState(state)?.decorations ?? DecorationSet.empty;
      },
    },
  });
}

export function useRichMarkdownSearch(editor: Editor | null) {
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [isReplaceMode, setIsReplaceMode] = useState(false);
  const [query, setQuery] = useState('');
  const [replaceQuery, setReplaceQuery] = useState('');
  const [matchCase, setMatchCase] = useState(false);
  const [wholeWord, setWholeWord] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const [docRevision, setDocRevision] = useState(0);

  useEffect(() => {
    if (!editor) return;
    const plugin = createRichMarkdownSearchPlugin();
    editor.registerPlugin(plugin);
    return () => {
      editor.unregisterPlugin(richMarkdownSearchPluginKey);
    };
  }, [editor]);

  useEffect(() => {
    if (!editor || !isOpen) return;
    const onUpdate = () => setDocRevision(r => r + 1);
    editor.on('update', onUpdate);
    return () => {
      editor.off('update', onUpdate);
    };
  }, [editor, isOpen]);

  const matches = useMemo(() => {
    void docRevision;
    if (!editor || editor.isDestroyed || !isOpen || !query) return [];
    return findRichMarkdownSearchMatches(editor.state.doc, query, { matchCase, wholeWord });
  }, [editor, isOpen, query, matchCase, wholeWord, docRevision]);

  const clampedIndex = matches.length === 0 ? -1 : Math.min(Math.max(0, activeIndex), matches.length - 1);

  useEffect(() => {
    if (!editor || editor.isDestroyed) return;
    const tr = editor.state.tr.setMeta(richMarkdownSearchPluginKey, {
      activeIndex: clampedIndex,
      matches: isOpen ? matches : [],
      query: isOpen ? query : '',
    });
    editor.view.dispatch(tr);
  }, [editor, isOpen, query, matches, clampedIndex]);

  const openSearch = useCallback((replace = false) => {
    if (replace) setIsReplaceMode(true);
    setIsOpen(true);
    requestAnimationFrame(() => {
      searchInputRef.current?.focus();
      searchInputRef.current?.select();
    });
  }, []);

  const closeSearch = useCallback(() => {
    setIsOpen(false);
    setIsReplaceMode(false);
    setQuery('');
    setReplaceQuery('');
    editor?.commands.focus();
  }, [editor]);

  const moveToMatch = useCallback(
    (direction: 1 | -1) => {
      if (!editor || matches.length === 0) return;
      const next = (Math.max(0, clampedIndex) + direction + matches.length) % matches.length;
      setActiveIndex(next);
      const target = matches[next];
      if (target) {
        const tr = editor.state.tr
          .setSelection(TextSelection.create(editor.state.doc, target.from, target.to))
          .scrollIntoView();
        editor.view.dispatch(tr);
      }
    },
    [editor, matches, clampedIndex],
  );

  const replaceCurrent = useCallback(() => {
    if (!editor || matches.length === 0) return;
    const target = matches[Math.max(0, clampedIndex)];
    if (!target) return;
    const tr = editor.state.tr;
    if (replaceQuery) {
      tr.insertText(replaceQuery, target.from, target.to);
    } else {
      tr.delete(target.from, target.to);
    }
    editor.view.dispatch(tr);
  }, [editor, matches, clampedIndex, replaceQuery]);

  const replaceAll = useCallback(() => {
    if (!editor || matches.length === 0) return;
    const tr = editor.state.tr;
    for (let i = matches.length - 1; i >= 0; i -= 1) {
      const m = matches[i];
      if (replaceQuery) {
        tr.insertText(replaceQuery, m.from, m.to);
      } else {
        tr.delete(m.from, m.to);
      }
    }
    editor.view.dispatch(tr);
  }, [editor, matches, replaceQuery]);

  return {
    isOpen,
    isReplaceMode,
    query,
    replaceQuery,
    matchCase,
    wholeWord,
    matchCount: matches.length,
    activeMatchIndex: clampedIndex,
    searchInputRef,
    openSearch,
    closeSearch,
    setQuery,
    setReplaceQuery,
    toggleMatchCase: () => setMatchCase(v => !v),
    toggleWholeWord: () => setWholeWord(v => !v),
    toggleReplaceMode: () => setIsReplaceMode(v => !v),
    moveToMatch,
    replaceCurrent,
    replaceAll,
  };
}

export function RichMarkdownSearchBar({
  search,
}: {
  search: ReturnType<typeof useRichMarkdownSearch>;
}): React.JSX.Element | null {
  if (!search.isOpen) return null;
  const noMatches = search.matchCount === 0;
  const keepFocus = (e: React.MouseEvent) => e.preventDefault();

  return (
    <div className="rich-markdown-search" onKeyDown={e => e.stopPropagation()}>
      <button
        type="button"
        className="rich-markdown-search-toggle"
        onMouseDown={keepFocus}
        onClick={search.toggleReplaceMode}
        title={search.isReplaceMode ? '收起替换' : '展开替换'}
      >
        {search.isReplaceMode ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
      </button>
      <div className="rich-markdown-search-rows">
        <div className="rich-markdown-search-row">
          <div className="rich-markdown-search-field">
            <input
              ref={search.searchInputRef}
              className="rich-markdown-search-input"
              value={search.query}
              placeholder="在文档中查找…"
              onChange={e => search.setQuery(e.target.value)}
              onKeyDown={e => {
                if (e.nativeEvent.isComposing) return;
                if (e.key === 'Enter' && e.shiftKey) {
                  e.preventDefault();
                  search.moveToMatch(-1);
                } else if (e.key === 'Enter') {
                  e.preventDefault();
                  search.moveToMatch(1);
                } else if (e.key === 'Escape') {
                  e.preventDefault();
                  search.closeSearch();
                }
              }}
            />
            <button
              type="button"
              className="rich-markdown-search-option"
              data-active={search.matchCase ? 'true' : undefined}
              onMouseDown={keepFocus}
              onClick={search.toggleMatchCase}
              title="区分大小写"
            >
              <CaseSensitive size={13} />
            </button>
            <button
              type="button"
              className="rich-markdown-search-option"
              data-active={search.wholeWord ? 'true' : undefined}
              onMouseDown={keepFocus}
              onClick={search.toggleWholeWord}
              title="全词匹配"
            >
              <WholeWord size={13} />
            </button>
          </div>
          <div className="rich-markdown-search-status">
            {search.query && noMatches
              ? '无结果'
              : `${noMatches ? 0 : search.activeMatchIndex + 1}/${search.matchCount}`}
          </div>
          <button
            type="button"
            className="rich-markdown-search-button"
            disabled={noMatches}
            onMouseDown={keepFocus}
            onClick={() => search.moveToMatch(-1)}
            title="上一个 (⇧Enter)"
          >
            <ChevronUp size={13} />
          </button>
          <button
            type="button"
            className="rich-markdown-search-button"
            disabled={noMatches}
            onMouseDown={keepFocus}
            onClick={() => search.moveToMatch(1)}
            title="下一个 (Enter)"
          >
            <ChevronDown size={13} />
          </button>
          <button
            type="button"
            className="rich-markdown-search-button"
            onMouseDown={keepFocus}
            onClick={search.closeSearch}
            title="关闭查找 (Esc)"
          >
            <X size={13} />
          </button>
        </div>

        {search.isReplaceMode && (
          <div className="rich-markdown-search-row">
            <div className="rich-markdown-search-field">
              <input
                className="rich-markdown-search-input"
                value={search.replaceQuery}
                placeholder="替换为…"
                onChange={e => search.setReplaceQuery(e.target.value)}
                onKeyDown={e => {
                  if (e.nativeEvent.isComposing) return;
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    search.replaceCurrent();
                  } else if (e.key === 'Escape') {
                    e.preventDefault();
                    search.closeSearch();
                  }
                }}
              />
            </div>
            <button
              type="button"
              className="rich-markdown-search-button"
              disabled={noMatches}
              onMouseDown={keepFocus}
              onClick={search.replaceCurrent}
              title="替换当前项"
            >
              <Replace size={13} />
            </button>
            <button
              type="button"
              className="rich-markdown-search-button"
              disabled={noMatches}
              onMouseDown={keepFocus}
              onClick={search.replaceAll}
              title="全部替换"
            >
              <ReplaceAll size={13} />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
