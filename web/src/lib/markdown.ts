import DOMPurify from 'dompurify';
import { marked } from 'marked';
import { t } from './i18n/index.svelte';

/** Links in the vault use a small, stable notation:
 *
 *    [[claim:<id>|the claim]]   a claim citation
 *    [[wiki:<id>|page title]]   a wiki page
 *    [[source:<hash>|a source]] a source
 *    [[a title]]                a claim finder query
 *
 *  The scanner deliberately does not use a regular expression: a citation label may itself contain
 *  brackets (`slice[2] = 99`, `[]int{1, 2, 3}`). Only `]]` ends a link; the first `|` separates the
 *  target from its alias.
 */
function replaceWikilinks(line: string, render: (target: string, alias?: string) => string): string {
  let out = '';
  let cursor = 0;
  while (cursor < line.length) {
    const start = line.indexOf('[[', cursor);
    if (start < 0) {
      out += line.slice(cursor);
      break;
    }
    const end = line.indexOf(']]', start + 2);
    if (end < 0) {
      out += line.slice(cursor);
      break;
    }
    const raw = line.slice(start + 2, end);
    const pipe = raw.indexOf('|');
    const target = pipe >= 0 ? raw.slice(0, pipe) : raw;
    const alias = pipe >= 0 ? raw.slice(pipe + 1) : undefined;
    out += line.slice(cursor, start) + render(target, alias);
    cursor = end + 2;
  }
  return out;
}

/** Trailing tag lines are page metadata, not prose. */
function stripTrailingTags(markdown: string): string {
  const lines = markdown.split('\n');
  let end = lines.length;
  while (end > 0) {
    const line = lines[end - 1].trim();
    if (line === '') {
      end--;
      continue;
    }
    if (/^(#[\p{L}\p{N}_/-]+\s*)+$/u.test(line)) {
      end--;
      continue;
    }
    break;
  }
  return lines.slice(0, end).join('\n').replace(/\n+$/, '');
}

export interface MarkdownOptions {
  /** Known claim topics: an inline code span naming one becomes a link to that topic filter. */
  topics?: string[];
  /** Turn `([1](#cite-1))` references into footnote links to the numbered Sources entries. */
  footnotes?: boolean;
  /** Render `[[…]]` as a link. Off for a CLAIM BODY: a claim has no links, so its brackets are code. */
  wikilinks?: boolean;
  /** The Claims list's topic filter: a claim link keeps it, so the list beside the detail stays filtered. */
  claimTopic?: string;
}

/** renderMarkdown converts vault Markdown to sanitized HTML with the app's link conventions. */
export function renderMarkdown(markdown: string | null | undefined, options: MarkdownOptions = {}): string {
  const claimTopic = options.claimTopic ? `?topic=${encodeURIComponent(options.claimTopic)}` : '';
  const renderLink = (target: string, alias: string | undefined, bracketed: boolean): string => {
    const value = target.trim();
    const label = (text: string) => text.replace(/[[\]]/g, '\\$&');

    if (value.startsWith('wiki:')) {
      const id = value.slice('wiki:'.length).trim();
      return `[${label(alias ?? id)}](#/wiki/${encodeURIComponent(id)})`;
    }
    if (value.startsWith('source:')) {
      const hash = value.slice('source:'.length).trim();
      return `[${label(alias ?? hash)}](#/sources/${encodeURIComponent(hash)})`;
    }
    if (value.startsWith('claim:')) {
      const id = value.slice('claim:'.length).trim();
      const text = alias ?? id;
      if (bracketed) {
        // A claim citation in running prose should read as a citation, not as an ordinary link.
        return `[\\[${label(text)}\\]](#/claims/${encodeURIComponent(id)}${claimTopic})`;
      }
      return `[${label(text)}](#/claims/${encodeURIComponent(id)}${claimTopic})`;
    }
    return `[${label(alias ?? value)}](#/claims?q=${encodeURIComponent(value)})`;
  };

  // The Sources block at the end of an answer is a list, not prose; citations there lose the
  // square brackets. All other citations keep them.
  const lines = stripTrailingTags(markdown ?? '').split('\n');
  let inSources = false;
  const withLinks = lines
    .map((line) => {
      if (/^(?:\*\*)?sources(?:\*\*)?:?/i.test(line.trim())) inSources = true;
      if (options.wikilinks === false) return line;
      return replaceWikilinks(line, (target, alias) => renderLink(target, alias, !inSources));
    })
    .join('\n');

  const html = marked.parse(withLinks, { async: false, gfm: true }) as string;
  const clean = DOMPurify.sanitize(html, { ADD_ATTR: ['target'] }).replace(
    /<a href="(https?:[^"]*)"/g,
    '<a href="$1" target="_blank" rel="noopener noreferrer"',
  );

  const topics = (options.topics ?? []).map((topic) => topic.trim()).filter(Boolean);
  if (topics.length === 0 && !options.footnotes) return clean;

  const document = new DOMParser().parseFromString(clean, 'text/html');
  if (topics.length > 0) {
    const known = new Set(topics);
    for (const code of document.querySelectorAll('code')) {
      if (code.closest('pre')) continue;
      const text = (code.textContent ?? '').trim();
      if (!known.has(text)) continue;
      const link = document.createElement('a');
      link.setAttribute('href', `#/claims?topic=${encodeURIComponent(text)}`);
      link.className = 'topic-code-link';
      code.replaceWith(link);
      link.appendChild(code);
    }
  }
  if (options.footnotes) annotateFootnotes(document);
  return document.body.innerHTML;
}

/** annotateFootnotes gives the numbered Sources list stable targets and labels the prose links.
 *  The visible numbers stay plain text; only the claim links in the bibliography navigate away. */
function annotateFootnotes(document: Document): void {
  for (const link of document.querySelectorAll<HTMLAnchorElement>('a[href^="#cite-"]')) {
    const number = (link.getAttribute('href') ?? '').slice('#cite-'.length);
    if (!number) continue;
    link.classList.add('cite-ref');
    link.setAttribute('aria-label', t('markdown.reference', { number }));
  }

  const heading = [...document.querySelectorAll('p')].find((paragraph) =>
    /^sources:?$/i.test((paragraph.textContent ?? '').trim()),
  );
  const list = heading?.nextElementSibling;
  if (!list || (list.tagName !== 'OL' && list.tagName !== 'UL')) return;
  [...list.children].forEach((item, index) => {
    const number = index + 1;
    item.id = `cite-${number}`;
    item.classList.add('cite-entry');
    const reference = document.querySelector<HTMLAnchorElement>(`a[href="#cite-${number}"]`);
    if (!reference) return;
    reference.id = `cite-ref-${number}`;
    const back = document.createElement('a');
    back.setAttribute('href', `#cite-ref-${number}`);
    back.setAttribute('aria-label', t('markdown.backToReference', { number }));
    back.className = 'cite-back';
    back.textContent = '↩';
    item.append(' ', back);
  });
}
