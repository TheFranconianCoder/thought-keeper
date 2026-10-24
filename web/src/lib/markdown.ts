import DOMPurify from 'dompurify';
import { marked } from 'marked';

// Wikilinks ([[Claim]] / [[Claim|alias]]) become in-app links before Markdown runs, so a claim's
// cross-references navigate the Claims lens.
//
// This is a SCANNER, not a regular expression: a citation label can itself contain square brackets
// (a claim about `slice[2] = 99` or `[]int{1, 2, 3}`), and a regex that stops at the first `]` leaves
// the whole citation as raw text. Only the closing `]]` ends the link; the first `|` separates the
// target from the alias.
function replaceWikilinks(
	line: string,
	render: (target: string, alias: string | undefined) => string,
): string {
	let out = '';
	let at = 0;
	while (at < line.length) {
		const start = line.indexOf('[[', at);
		if (start < 0) {
			out += line.slice(at);
			break;
		}
		const end = line.indexOf(']]', start + 2);
		if (end < 0) {
			out += line.slice(at);
			break;
		}
		const raw = line.slice(start + 2, end);
		const pipe = raw.indexOf('|');
		const target = pipe >= 0 ? raw.slice(0, pipe) : raw;
		const alias = pipe >= 0 ? raw.slice(pipe + 1) : undefined;
		out += line.slice(at, start) + render(target, alias);
		at = end + 2;
	}
	return out;
}

/** stripTrailingTags drops trailing lines that are only tags (they belong to the
 * page metadata, not the prose). */
function stripTrailingTags(md: string): string {
	const lines = md.split('\n');
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

/** Topics may be rendered as inline code in an answer; a known topic becomes a link to its filter. */
export interface MarkdownOptions {
	topics?: string[];
}

/** mdToHtml renders Markdown to sanitized HTML with the app's link conventions. */
export function mdToHtml(md: string | undefined | null, opts: MarkdownOptions = {}): string {
	const renderLink = (target: string, alias: string | undefined, bracketed: boolean): string => {
		const title = String(target).trim();
		// A claim citation is an id, not a page title: [[claim:<id>|the claim]].
		if (title.startsWith('wiki:')) {
			const id = title.slice('wiki:'.length).trim();
			const label = (alias ?? id).replace(/[\[\]]/g, '\\$&');
			return `[${label}](#/wiki/${encodeURIComponent(id)})`;
		}
		if (title.startsWith('source:')) {
			const id = title.slice('source:'.length).trim();
			const label = (alias ?? id).replace(/[\[\]]/g, '\\$&');
			return `[${label}](#/sources/${encodeURIComponent(id)})`;
		}
		if (title.startsWith('claim:')) {
			const id = title.slice('claim:'.length).trim();
			const label = alias ?? id;
			if (bracketed) {
				// A claim citation must READ as a citation in running prose. A bare hyperlink would look
				// like ordinary text; the square brackets are part of the visible link label.
				const escaped = label.replace(/[\[\]]/g, '\\$&');
				return `[\\[${escaped}\\]](#/claims/${encodeURIComponent(id)})`;
			}
			// The Sources block is a list, not running prose: no brackets there.
			return `[${label}](#/claims/${encodeURIComponent(id)})`;
		}
		return `[${alias ?? title}](#/claims?q=${encodeURIComponent(title)})`;
	};

	// The Sources block at the end of an answer is a list, not prose. Its citations stay readable as
	// plain links; the square brackets belong only to citations inside the running text.
	const lines = stripTrailingTags(md ?? '').split('\n');
	let inSources = false;
	const src = lines
		.map((line) => {
			const trimmed = line.trim().toLowerCase();
			if (/^(?:\*\*)?sources(?:\*\*)?:?/.test(trimmed)) inSources = true;
			return replaceWikilinks(line, (target, alias) => renderLink(target, alias, !inSources));
		})
		.join('\n');

	const raw = marked.parse(src, { async: false, gfm: true }) as string;
	const clean = DOMPurify.sanitize(raw, { ADD_ATTR: ['target'] })
		.replace(/<a href="(https?:[^"]*)"/g, '<a href="$1" target="_blank" rel="noopener"');
	const topics = (opts.topics ?? []).map((t) => t.trim()).filter(Boolean);
	if (topics.length === 0) return clean;

	// The model sometimes names a topic as inline code. When that word is a real claim topic, make
	// the code span itself the link to the topic filter — never touch a code block.
	const body = new DOMParser().parseFromString(clean, 'text/html').body;
	const known = new Set(topics);
	for (const code of body.querySelectorAll('code')) {
		if (code.closest('pre')) continue;
		const text = (code.textContent ?? '').trim();
		if (!known.has(text)) continue;
		const link = body.ownerDocument.createElement('a');
		link.setAttribute('href', '#/claims?topic=' + encodeURIComponent(text));
		link.className = 'topic-code-link';
		code.replaceWith(link);
		link.appendChild(code);
	}
	return body.innerHTML;
}
