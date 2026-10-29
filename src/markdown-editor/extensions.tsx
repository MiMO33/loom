import React, { useCallback, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import {
  decodeHtmlEntities,
  getChangedRanges,
  type AnyExtension,
  type Editor,
  type MarkdownLexerConfiguration,
  type MarkdownParseHelpers,
  type MarkdownParseResult,
  type MarkdownToken,
  type MarkdownTokenizer,
} from '@tiptap/core';
import StarterKit from '@tiptap/starter-kit';
import { Paragraph } from '@tiptap/extension-paragraph';
import { TrailingNode } from '@tiptap/extensions';
import Link from '@tiptap/extension-link';
import { Code } from '@tiptap/extension-code';
import { CodeBlock } from '@tiptap/extension-code-block';
import CodeBlockLowlight, { type CodeBlockLowlightOptions } from '@tiptap/extension-code-block-lowlight';
import Image from '@tiptap/extension-image';
import Placeholder from '@tiptap/extension-placeholder';
import { OrderedList, ORDERED_LIST_MARKER_PATTERN, TaskItem, TaskList } from '@tiptap/extension-list';
import { Table, TableCell, TableHeader, TableRow } from '@tiptap/extension-table';
import { Details, DetailsContent, DetailsSummary } from '@tiptap/extension-details';
import { BlockMath, InlineMath } from '@tiptap/extension-mathematics';
import { Markdown } from '@tiptap/markdown';
import { NodeViewContent, NodeViewWrapper, ReactNodeViewRenderer, type NodeViewProps } from '@tiptap/react';
import type { Node as ProseMirrorNode } from '@tiptap/pm/model';
import { Plugin, PluginKey, TextSelection, type Transaction } from '@tiptap/pm/state';
import { Decoration, DecorationSet } from '@tiptap/pm/view';
import { createLowlight, common } from 'lowlight';
import { Check, Copy } from 'lucide-react';
import { copyText } from '../api';

export type ToggleHeadingVariant =
  | 'heading-1'
  | 'heading-2'
  | 'heading-3'
  | 'heading-4'
  | 'heading-5';

export function parseToggleHeadingVariant(value: unknown): ToggleHeadingVariant | null {
  return value === 'heading-1' ||
    value === 'heading-2' ||
    value === 'heading-3' ||
    value === 'heading-4' ||
    value === 'heading-5'
    ? value
    : null;
}

const CODE_LANGUAGES: readonly { value: string; label: string }[] = [
  { value: '', label: 'Plain text' },
  { value: 'bash', label: 'Bash' },
  { value: 'c', label: 'C' },
  { value: 'cpp', label: 'C++' },
  { value: 'css', label: 'CSS' },
  { value: 'diff', label: 'Diff' },
  { value: 'go', label: 'Go' },
  { value: 'graphql', label: 'GraphQL' },
  { value: 'html', label: 'HTML' },
  { value: 'java', label: 'Java' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'json', label: 'JSON' },
  { value: 'kotlin', label: 'Kotlin' },
  { value: 'markdown', label: 'Markdown' },
  { value: 'mermaid', label: 'Mermaid' },
  { value: 'python', label: 'Python' },
  { value: 'ruby', label: 'Ruby' },
  { value: 'rust', label: 'Rust' },
  { value: 'scss', label: 'SCSS' },
  { value: 'shell', label: 'Shell' },
  { value: 'sql', label: 'SQL' },
  { value: 'swift', label: 'Swift' },
  { value: 'typescript', label: 'TypeScript' },
  { value: 'xml', label: 'XML' },
  { value: 'yaml', label: 'YAML' },
];

function getCodeBlockLanguageLabel(value: string): string {
  return CODE_LANGUAGES.find(lang => lang.value === value)?.label ?? (value || 'Plain text');
}

function isKnownCodeBlockLanguage(value: string): boolean {
  return CODE_LANGUAGES.some(lang => lang.value === value);
}

export function RichMarkdownCodeBlock({ node, updateAttributes }: NodeViewProps): React.JSX.Element {
  const language = (node.attrs.language as string) || '';
  const [copied, setCopied] = useState(false);
  const [languageListMounted, setLanguageListMounted] = useState(false);
  const timerRef = useRef<number | null>(null);

  const mountLanguageList = useCallback(() => {
    if (!languageListMounted) {
      flushSync(() => setLanguageListMounted(true));
    }
  }, [languageListMounted]);

  const handleCopy = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      void copyText(node.textContent).then(() => {
        if (timerRef.current !== null) window.clearTimeout(timerRef.current);
        setCopied(true);
        timerRef.current = window.setTimeout(() => {
          timerRef.current = null;
          setCopied(false);
        }, 1500);
      });
    },
    [node],
  );

  return (
    <NodeViewWrapper className="rich-markdown-code-block-wrapper">
      <select
        className="rich-markdown-code-block-lang"
        contentEditable={false}
        value={language}
        onChange={e => updateAttributes({ language: e.target.value })}
        onMouseDown={mountLanguageList}
        onFocus={mountLanguageList}
      >
        {languageListMounted ? (
          CODE_LANGUAGES.map(lang => (
            <option key={lang.value} value={lang.value}>
              {lang.label}
            </option>
          ))
        ) : (
          <option value={language}>{getCodeBlockLanguageLabel(language)}</option>
        )}
        {languageListMounted && language && !isKnownCodeBlockLanguage(language) ? (
          <option key={language} value={language}>
            {language}
          </option>
        ) : null}
      </select>
      <button
        type="button"
        className="code-block-copy-btn"
        contentEditable={false}
        onClick={handleCopy}
        aria-label="复制代码"
        title="复制代码"
      >
        {copied ? (
          <>
            <Check size={13} />
            <span className="code-block-copy-label">已复制</span>
          </>
        ) : (
          <Copy size={13} />
        )}
      </button>
      <NodeViewContent<'pre'> as="pre" />
    </NodeViewWrapper>
  );
}

const lowlight = createLowlight(common);

type Lowlight = ReturnType<typeof createLowlight>;
type HighlightNode = ReturnType<Lowlight['highlight']>['children'][number];
type CodeBlockAt = { node: ProseMirrorNode; pos: number };
type DocumentRange = { from: number; to: number };
type HighlightSpan = { text: string; classes: string[] };

function parseHighlightNodes(nodes: HighlightNode[], classes: string[] = []): HighlightSpan[] {
  return nodes.flatMap(node => {
    if (node.type === 'text') {
      return [{ text: node.value, classes }];
    }
    if (node.type !== 'element') {
      return [];
    }
    const className = node.properties.className;
    const ownClasses = Array.isArray(className)
      ? className.map(String)
      : typeof className === 'string'
        ? [className]
        : [];
    return parseHighlightNodes(node.children, [...classes, ...ownClasses]);
  });
}

function highlightBlock(
  block: CodeBlockAt,
  ll: Lowlight,
  languages: ReadonlySet<string>,
  defaultLanguage: string | null | undefined,
): Decoration[] {
  const language = (block.node.attrs.language as string | null | undefined) || defaultLanguage;
  const highlighted =
    language && (languages.has(language) || ll.registered(language))
      ? ll.highlight(language, block.node.textContent)
      : ll.highlightAuto(block.node.textContent);
  const decorations: Decoration[] = [];
  let from = block.pos + 1;
  for (const span of parseHighlightNodes(highlighted.children)) {
    const to = from + span.text.length;
    if (span.classes.length > 0) {
      decorations.push(Decoration.inline(from, to, { class: span.classes.join(' ') }));
    }
    from = to;
  }
  return decorations;
}

function findAllCodeBlocks(doc: ProseMirrorNode, name: string): CodeBlockAt[] {
  const blocks: CodeBlockAt[] = [];
  doc.descendants((node, pos) => {
    if (node.type.name === name) {
      blocks.push({ node, pos });
      return false;
    }
    return true;
  });
  return blocks;
}

function findCodeBlocksNearRanges(doc: ProseMirrorNode, name: string, ranges: DocumentRange[]): CodeBlockAt[] {
  const blocks = new Map<number, ProseMirrorNode>();
  const addResolvedBlocks = (position: number): void => {
    const $position = doc.resolve(Math.max(0, Math.min(doc.content.size, position)));
    for (let depth = $position.depth; depth > 0; depth -= 1) {
      const node = $position.node(depth);
      if (node.type.name === name) {
        blocks.set($position.before(depth), node);
        break;
      }
    }
    if ($position.nodeBefore?.type.name === name) {
      blocks.set($position.pos - $position.nodeBefore.nodeSize, $position.nodeBefore);
    }
    if ($position.nodeAfter?.type.name === name) {
      blocks.set($position.pos, $position.nodeAfter);
    }
  };
  for (const range of ranges) {
    const from = Math.max(0, Math.min(doc.content.size, range.from));
    const to = Math.max(from, Math.min(doc.content.size, range.to));
    if (from < to) {
      doc.nodesBetween(from, to, (node, pos) => {
        if (node.type.name === name) {
          blocks.set(pos, node);
          return false;
        }
        return true;
      });
    }
    addResolvedBlocks(from);
    if (to !== from) addResolvedBlocks(to);
  }
  return [...blocks].map(([pos, node]) => ({ node, pos }));
}

function createDecorations(
  doc: ProseMirrorNode,
  name: string,
  ll: Lowlight,
  languages: ReadonlySet<string>,
  defaultLanguage: string | null | undefined,
): DecorationSet {
  return DecorationSet.create(
    doc,
    findAllCodeBlocks(doc, name).flatMap(block => highlightBlock(block, ll, languages, defaultLanguage)),
  );
}

function updateDecorations(
  transaction: Transaction,
  decorationSet: DecorationSet,
  name: string,
  ll: Lowlight,
  languages: ReadonlySet<string>,
  defaultLanguage: string | null | undefined,
): DecorationSet {
  if (!transaction.docChanged) return decorationSet;
  const changes = getChangedRanges(transaction);
  if (changes.length === 0) {
    return createDecorations(transaction.doc, name, ll, languages, defaultLanguage);
  }
  let next = decorationSet.map(transaction.mapping, transaction.doc);
  const oldBlocks = findCodeBlocksNearRanges(
    transaction.before,
    name,
    changes.map(c => c.oldRange),
  );
  const stale = new Set<Decoration>();
  for (const block of oldBlocks) {
    const from = transaction.mapping.map(block.pos, -1);
    const to = transaction.mapping.map(block.pos + block.node.nodeSize, 1);
    for (const dec of next.find(Math.min(from, to), Math.max(from, to))) {
      stale.add(dec);
    }
  }
  if (stale.size > 0) next = next.remove([...stale]);
  const newBlocks = findCodeBlocksNearRanges(
    transaction.doc,
    name,
    changes.map(c => c.newRange),
  );
  return next.add(
    transaction.doc,
    newBlocks.flatMap(block => highlightBlock(block, ll, languages, defaultLanguage)),
  );
}

export const RichMarkdownCodeBlockLowlight = CodeBlock.extend<CodeBlockLowlightOptions>({
  addOptions() {
    return { ...CodeBlockLowlight.options };
  },
  addProseMirrorPlugins() {
    const ll = this.options.lowlight as Lowlight;
    const languages = new Set(ll.listLanguages());
    const key = new PluginKey<DecorationSet>('richMarkdownLowlight');
    return [
      ...(this.parent?.() ?? []),
      new Plugin<DecorationSet>({
        key,
        state: {
          init: (_, { doc }) => createDecorations(doc, this.name, ll, languages, this.options.defaultLanguage),
          apply: (tr, set) => updateDecorations(tr, set, this.name, ll, languages, this.options.defaultLanguage),
        },
        props: {
          decorations: state => key.getState(state),
        },
      }),
    ];
  },
});

const baseParseMarkdown = Paragraph.config.parseMarkdown as
  | ((token: MarkdownToken, helpers: MarkdownParseHelpers) => MarkdownParseResult)
  | undefined;

export const RichMarkdownParagraph = Paragraph.extend({
  parseMarkdown: (token, helpers) => {
    const tokens = token.tokens ?? [];
    if (!baseParseMarkdown || (tokens.length === 1 && tokens[0]?.type === 'image')) {
      return helpers.createNode('paragraph', undefined, helpers.parseInline(tokens));
    }
    return baseParseMarkdown(token, helpers);
  },
});

const trailingParagraphKey = new PluginKey<ProseMirrorNode | null>('richMarkdownTrailingParagraph');

export const RichMarkdownTrailingParagraph = TrailingNode.extend({
  addProseMirrorPlugins() {
    const plugins = (this.parent?.() ?? []).map(plugin => {
      const append = plugin.spec.appendTransaction;
      if (!append) return plugin;
      return new Plugin({
        ...plugin.spec,
        appendTransaction(transactions, oldState, state) {
          const tr = append.call(plugin, transactions, oldState, state);
          if (tr) tr.setMeta(trailingParagraphKey, tr.doc.lastChild);
          return tr;
        },
      });
    });
    return [
      ...plugins,
      new Plugin<ProseMirrorNode | null>({
        key: trailingParagraphKey,
        state: {
          init: () => null,
          apply(tr, previous, oldState) {
            const inserted: unknown = tr.getMeta(trailingParagraphKey);
            if (inserted === tr.doc.lastChild && tr.doc.lastChild) {
              return tr.doc.lastChild;
            }
            if (!previous || !tr.docChanged) return previous;
            let start = oldState.doc.content.size - previous.nodeSize;
            let touched = false;
            for (const map of tr.mapping.maps) {
              map.forEach((from, to) => {
                if (from <= start + previous.nodeSize && to > start) touched = true;
              });
              start = map.map(start, 1);
            }
            return !touched && tr.doc.lastChild === previous ? previous : null;
          },
        },
      }),
    ];
  },
});

const orderedListStart = new RegExp(`^\\s*(?:${ORDERED_LIST_MARKER_PATTERN})[.)]\\s`);
const baseOrderedListTokenizer = OrderedList.config.markdownTokenizer as MarkdownTokenizer;

export const RichMarkdownOrderedList = OrderedList.extend({
  addAttributes() {
    const { type: _letteredType, ...attributes } = (this.parent?.() ?? {}) as Record<string, unknown>;
    return attributes;
  },
  markdownTokenizer: {
    ...baseOrderedListTokenizer,
    tokenize(src, tokens, lexer) {
      if (!orderedListStart.test(src)) return undefined;
      return baseOrderedListTokenizer.tokenize(src, tokens, lexer);
    },
  },
});

const baseTaskListTokenizer = TaskList.config.markdownTokenizer as MarkdownTokenizer;

function normalizeTaskListToken(token: MarkdownToken, lexer: MarkdownLexerConfiguration): void {
  const firstNested = token.nestedTokens?.[0];
  if (
    token.type === 'taskItem' &&
    firstNested?.type === 'code' &&
    firstNested.codeBlockStyle === 'indented' &&
    typeof firstNested.text === 'string'
  ) {
    firstNested.type = 'paragraph';
    firstNested.raw = firstNested.text;
    firstNested.tokens = lexer.inlineTokens(firstNested.text);
    delete firstNested.codeBlockStyle;
  }
  for (const child of [...(token.items ?? []), ...(token.nestedTokens ?? [])]) {
    normalizeTaskListToken(child, lexer);
  }
}

export const RichMarkdownTaskList = TaskList.extend({
  markdownTokenizer: {
    ...baseTaskListTokenizer,
    tokenize(src, tokens, lexer) {
      if (typeof baseTaskListTokenizer.start === 'function' && baseTaskListTokenizer.start(src) !== 0) {
        return undefined;
      }
      const token = baseTaskListTokenizer.tokenize(src, tokens, lexer);
      if (token) normalizeTaskListToken(token, lexer);
      return token;
    },
  },
});

const tableTokenizer = Table.config.markdownTokenizer;

export const RichMarkdownTable = Table.extend({
  markdownTokenizer:
    tableTokenizer && typeof tableTokenizer !== 'function'
      ? {
          ...tableTokenizer,
          start(source) {
            const firstEnd = source.indexOf('\n');
            if (firstEnd === -1) return -1;
            const secondEnd = source.indexOf('\n', firstEnd + 1);
            const prefix = secondEnd === -1 ? source : source.slice(0, secondEnd);
            return typeof tableTokenizer.start === 'function' ? tableTokenizer.start(prefix) : -1;
          },
        }
      : tableTokenizer,
});

const RichMarkdownCode = Code.extend({
  excludes: 'code bold italic strike underline',
});

export function moveDetailsSummarySelectionToContent(editor: Editor): boolean {
  const { state, view } = editor;
  const { $from, empty } = state.selection;
  if (!empty || $from.parent.type.name !== 'detailsSummary') return false;
  const detailsDepth = $from.depth - 1;
  if (detailsDepth < 1) return false;
  const detailsNode = $from.node(detailsDepth);
  if (detailsNode.type.name !== 'details' || detailsNode.attrs.open === false) return false;
  const detailsContent = detailsNode.child(1);
  if (detailsContent?.type.name !== 'detailsContent') return false;
  const detailsStart = $from.before(detailsDepth);
  const detailsContentStart = detailsStart + 1 + detailsNode.child(0).nodeSize;
  const firstBodyNode = detailsContent.firstChild;
  if (!firstBodyNode?.isTextblock) return false;
  const targetPos = detailsContentStart + 2;
  const tr = state.tr.setSelection(TextSelection.near(state.doc.resolve(targetPos), 1)).scrollIntoView();
  view.dispatch(tr);
  return true;
}

export function exitEmptyDetailsBody(editor: Editor): boolean {
  const { state, view } = editor;
  const { $from, empty } = state.selection;
  if (!empty || !$from.parent.isTextblock || $from.parent.content.size !== 0) return false;
  const detailsContentDepth = $from.depth - 1;
  if (detailsContentDepth < 1) return false;
  const detailsContentNode = $from.node(detailsContentDepth);
  if (detailsContentNode.type.name !== 'detailsContent') return false;
  const childIndex = $from.index(detailsContentDepth);
  if (childIndex !== detailsContentNode.childCount - 1) return false;
  const detailsDepth = detailsContentDepth - 1;
  const detailsNode = $from.node(detailsDepth);
  if (detailsNode.type.name !== 'details') return false;
  const paragraph = state.schema.nodes.paragraph?.createAndFill();
  if (!paragraph) return false;
  const currentBlockFrom = $from.before($from.depth);
  const currentBlockTo = $from.after($from.depth);
  const shouldRemoveTrailingEmptyBlock = detailsContentNode.childCount > 1;
  let insertPos = $from.after(detailsDepth);
  const tr = state.tr;
  if (shouldRemoveTrailingEmptyBlock) {
    tr.delete(currentBlockFrom, currentBlockTo);
    insertPos -= currentBlockTo - currentBlockFrom;
  }
  tr.insert(insertPos, paragraph);
  tr.setSelection(TextSelection.create(tr.doc, insertPos + 1)).scrollIntoView();
  view.dispatch(tr);
  return true;
}

export function moveFromEmptyDetailsBodyToSummary(editor: Editor): boolean {
  const { state, view } = editor;
  const { $from, empty } = state.selection;
  if (!empty || !$from.parent.isTextblock || $from.parent.content.size !== 0 || $from.parentOffset !== 0) {
    return false;
  }
  const detailsContentDepth = $from.depth - 1;
  if (detailsContentDepth < 1) return false;
  const detailsContentNode = $from.node(detailsContentDepth);
  if (
    detailsContentNode.type.name !== 'detailsContent' ||
    detailsContentNode.childCount !== 1 ||
    $from.index(detailsContentDepth) !== 0
  ) {
    return false;
  }
  const detailsDepth = detailsContentDepth - 1;
  const detailsNode = $from.node(detailsDepth);
  if (detailsNode.type.name !== 'details' || detailsNode.childCount < 2) return false;
  const summaryNode = detailsNode.child(0);
  if (summaryNode.type.name !== 'detailsSummary') return false;
  const detailsStart = $from.before(detailsDepth);
  const summaryTextEnd = detailsStart + 1 + summaryNode.nodeSize - 1;
  const tr = state.tr.setSelection(TextSelection.near(state.doc.resolve(summaryTextEnd), -1)).scrollIntoView();
  view.dispatch(tr);
  return true;
}

function escapeDetailsHtml(text: string): string {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

const OrcaDetails = Details.extend({
  priority: 1000,
  addAttributes() {
    return {
      ...this.parent?.(),
      variant: {
        default: null,
        parseHTML: element => parseToggleHeadingVariant(element.getAttribute('data-orca-toggle')),
        renderHTML: ({ variant }) => {
          const parsed = parseToggleHeadingVariant(variant);
          return parsed ? { 'data-orca-toggle': parsed } : {};
        },
      },
    };
  },
  addKeyboardShortcuts() {
    const parentShortcuts = this.parent?.() ?? {};
    return {
      ...parentShortcuts,
      Enter: ({ editor }) => moveDetailsSummarySelectionToContent(editor) || parentShortcuts.Enter?.({ editor }) || false,
    };
  },
  renderMarkdown: (node, helpers) => {
    const summary = node.content?.find(child => child.type === 'detailsSummary');
    const content = node.content?.find(child => child.type === 'detailsContent');
    const summaryText = escapeDetailsHtml(decodeHtmlEntities(helpers.renderChildren(summary?.content ?? [], '')));
    const body = helpers.renderChildren(content?.content ?? [], '\n\n').trim();
    const parts = ['open'];
    const variant = parseToggleHeadingVariant(node.attrs?.variant);
    if (variant) parts.push(`data-orca-toggle="${variant}"`);
    return `<details ${parts.join(' ')}>\n<summary>${summaryText}</summary>\n\n${body}\n\n</details>`;
  },
});

const OrcaDetailsSummary = DetailsSummary.extend({
  content: 'inline*',
});

const OrcaDetailsContent = DetailsContent.extend({
  priority: 1000,
  addKeyboardShortcuts() {
    const parentShortcuts = this.parent?.() ?? {};
    return {
      ...parentShortcuts,
      Enter: ({ editor }) => exitEmptyDetailsBody(editor) || parentShortcuts.Enter?.({ editor }) || false,
      Backspace: ({ editor }) =>
        moveFromEmptyDetailsBodyToSummary(editor) || parentShortcuts.Backspace?.({ editor }) || false,
    };
  },
});

export const RichMarkdownExtension = Markdown.extend({
  onBeforeCreate(event) {
    this.parent?.(event);
    if (this.editor.options.contentType === 'markdown' && typeof this.editor.options.content === 'string') {
      if (!this.editor.options.content.trim()) {
        this.editor.options.content = this.editor.schema.topNodeType.createAndFill()!.toJSON();
      }
    }
  },
});

const TOGGLE_HEADING_LABELS: Record<ToggleHeadingVariant, string> = {
  'heading-1': '折叠标题 1',
  'heading-2': '折叠标题 2',
  'heading-3': '折叠标题 3',
  'heading-4': '折叠标题 4',
  'heading-5': '折叠标题 5',
};

const RichMarkdownImage = Image.extend({
  renderHTML({ HTMLAttributes }) {
    const resolveImageSrc = (
      this.options as { resolveImageSrc?: (src: string) => string }
    ).resolveImageSrc;
    const rawSrc = typeof HTMLAttributes.src === 'string' ? HTMLAttributes.src : '';
    const src = rawSrc && resolveImageSrc ? resolveImageSrc(rawSrc) : HTMLAttributes.src;
    return ['img', { ...this.options.HTMLAttributes, ...HTMLAttributes, ...(src ? { src } : {}) }];
  },
});

export function createRichMarkdownExtensions(
  placeholderText = '输入 Markdown 内容，或输入 / 插入块…',
  resolveImageSrc?: (src: string) => string,
): AnyExtension[] {
  return [
    StarterKit.configure({
      link: false,
      code: false,
      codeBlock: false,
      orderedList: false,
      paragraph: false,
      trailingNode: false,
    }),
    RichMarkdownParagraph,
    RichMarkdownTrailingParagraph,
    RichMarkdownCode,
    RichMarkdownCodeBlockLowlight.extend({
      addNodeView() {
        return ReactNodeViewRenderer(RichMarkdownCodeBlock);
      },
    }).configure({
      lowlight,
      defaultLanguage: null,
    }),
    Link.configure({
      openOnClick: false,
      autolink: true,
      linkOnPaste: true,
    }),
    RichMarkdownImage.configure({
      inline: true,
      allowBase64: true,
      ...(resolveImageSrc ? { resolveImageSrc } : {}),
    } as Record<string, unknown>),
    RichMarkdownOrderedList,
    RichMarkdownTaskList,
    TaskItem.configure({ nested: true }),
    RichMarkdownTable.configure({ resizable: false }),
    TableRow,
    TableHeader,
    TableCell,
    BlockMath,
    InlineMath,
    OrcaDetails.configure({
      persist: true,
      HTMLAttributes: { class: 'orca-details' },
    }),
    OrcaDetailsSummary,
    OrcaDetailsContent,
    Placeholder.configure({
      includeChildren: true,
      placeholder: ({ editor, node, pos }) => {
        if (node.type.name === 'detailsSummary') {
          const parent = editor.state.doc.resolve(pos).parent;
          const variant = parent.type.name === 'details' ? parseToggleHeadingVariant(parent.attrs.variant) : null;
          return variant ? TOGGLE_HEADING_LABELS[variant] : '折叠区块标题…';
        }
        return placeholderText;
      },
    }),
    RichMarkdownExtension.configure({
      markedOptions: { gfm: true, breaks: false },
    }),
  ];
}
