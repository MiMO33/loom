const FRONTMATTER_RE = /^(---|\+\+\+)\r?\n(?:[\s\S]*?\r?\n)?\1(?:\r?\n|$)/;

export type FrontMatter = {
  raw: string;
  body: string;
};

export type MarkdownTocItem = {
  id: string;
  level: number;
  text: string;
  line: number;
};

export function extractFrontMatter(content: string): FrontMatter | null {
  const match = content.match(FRONTMATTER_RE);
  if (!match) return null;
  const raw = match[0];
  const body = content.slice(raw.length);
  return { raw, body };
}

export function prependFrontMatter(raw: string, body: string): string {
  const normalizedRaw = raw.endsWith('\n') ? raw : `${raw}\n`;
  return `${normalizedRaw}${body}`;
}

export function markdownFrontMatterInner(frontMatter: FrontMatter | null): string {
  return (
    frontMatter?.raw
      .replace(/^(?:---|\+\+\+)\r?\n/, '')
      .replace(/\r?\n(?:---|\+\+\+)\r?\n?$/, '')
      .trim() ?? ''
  );
}

export function extractTableOfContents(markdown: string): MarkdownTocItem[] {
  const items: MarkdownTocItem[] = [];
  const lines = markdown.split(/\r?\n/);
  let inFence = false;
  let fenceMarker = '';

  lines.forEach((line, index) => {
    const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
    if (fenceMatch) {
      const marker = fenceMatch[1][0];
      if (!inFence) {
        inFence = true;
        fenceMarker = marker;
      } else if (marker === fenceMarker) {
        inFence = false;
      }
      return;
    }
    if (inFence) return;

    const headingMatch = line.match(/^(#{1,6})\s+(.+?)\s*#*$/);
    if (!headingMatch) return;
    const level = headingMatch[1].length;
    const text = headingMatch[2].replace(/[`*_~[\]]/g, '').trim();
    if (!text) return;
    items.push({
      id: `heading-${index}-${text.toLowerCase().replace(/\s+/g, '-')}`,
      level,
      text,
      line: index + 1,
    });
  });

  return items;
}
