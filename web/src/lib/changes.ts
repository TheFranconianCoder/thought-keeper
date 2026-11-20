/** Reading a source's change record.
 *
 *  `<hash>.changes.md` is a RECORD, not a cache: it names the last event of a URL page and the diff
 *  of that event. This module turns it into the view a reader needs — a summary and hunks whose
 *  lines know whether they were added, removed or kept — instead of rendering its Markdown bytes.
 *
 *  The record stays the one format (see docs/storage.md). Parsing is deliberately tolerant: without
 *  the `# Changed` header there is no record and the tab keeps the raw text, an unknown sentence is
 *  ignored, and a broken hunk is dropped. The tab never shows half a story.
 *
 *  The word-level emphasis is the readable half of a structural diff: within a change block the
 *  paired lines are compared token by token, so a paragraph that gained one word marks that word
 *  instead of painting the whole line. No external diff binary is involved.
 */

export type ChangeLineKind = 'context' | 'add' | 'remove';

/** One token of a line, and whether that token is part of the difference. */
export interface ChangeSegment {
  text: string;
  changed: boolean;
}

export interface ChangeLine {
  kind: ChangeLineKind;
  /** 1-based line number in the body before the change, when the line exists there. */
  oldNo: number | null;
  /** 1-based line number in the body after the change, when the line exists there. */
  newNo: number | null;
  text: string;
  /** Tokens with word-level emphasis; absent when the line has no counterpart to compare with. */
  segments?: ChangeSegment[];
}

export interface ChangeHunk {
  oldStart: number;
  oldCount: number;
  newStart: number;
  newCount: number;
  lines: ChangeLine[];
}

export interface ChangeRecord {
  /** The instant the record names, as it was written (RFC 3339). */
  at: string;
  titleBefore: string;
  titleAfter: string;
  /** Whole-change line counts, as the record states them (not only the lines inside the hunks). */
  added: number;
  removed: number;
  /** The measurement that made the change an event; absent when the body did not move. */
  words: number | null;
  avgChars: number | null;
  /** What happened to the body: a real difference, nothing, or whitespace alone. */
  body: 'changed' | 'unchanged' | 'whitespace';
  /** The record stopped before the whole diff (its own note says so). */
  truncated: boolean;
  hunks: ChangeHunk[];
}

const TITLE = /^Title: "(.*)" → "(.*)"$/;
const BODY_COUNTS = /^Body: \+(\d+) -(\d+) lines\.$/;
const WORDS = /^Words: (\d+) changed, ([\d.]+) characters on average\.$/;
const HUNK = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/;

/** parseChangeRecord reads a `.changes.md` file into the change report, or null when it is not one. */
export function parseChangeRecord(text: string): ChangeRecord | null {
  const lines = text.replace(/\r\n/g, '\n').split('\n');
  const at = lines.find((line) => line.startsWith('# Changed '))?.slice('# Changed '.length).trim();
  if (!at) return null;

  const record: ChangeRecord = {
    at,
    titleBefore: '',
    titleAfter: '',
    added: 0,
    removed: 0,
    words: null,
    avgChars: null,
    body: 'changed',
    truncated: false,
    hunks: [],
  };

  let diff: string[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const title = TITLE.exec(line);
    if (title) {
      record.titleBefore = title[1];
      record.titleAfter = title[2];
      continue;
    }
    const counts = BODY_COUNTS.exec(line);
    if (counts) {
      record.added = Number(counts[1]);
      record.removed = Number(counts[2]);
      continue;
    }
    const words = WORDS.exec(line);
    if (words) {
      record.words = Number(words[1]);
      record.avgChars = Number(words[2]);
      continue;
    }
    if (line === 'Body: unchanged.') {
      record.body = 'unchanged';
      continue;
    }
    if (line.startsWith('Body: changed only in whitespace')) {
      record.body = 'whitespace';
      continue;
    }
    if (line.startsWith('The diff stops early')) {
      record.truncated = true;
      continue;
    }
    // The fence is four backticks (a page may contain three), opened as ````diff.
    if (line.startsWith('````diff')) {
      const body: string[] = [];
      for (i++; i < lines.length && lines[i] !== '````'; i++) body.push(lines[i]);
      diff = body;
    }
  }

  record.hunks = parseHunks(diff);
  if (record.added === 0 && record.removed === 0) {
    for (const hunk of record.hunks) {
      for (const line of hunk.lines) {
        if (line.kind === 'add') record.added++;
        if (line.kind === 'remove') record.removed++;
      }
    }
  }
  return record;
}

/** parseHunks turns the unified diff into numbered, typed lines and pairs each change block. */
function parseHunks(diff: string[]): ChangeHunk[] {
  const hunks: ChangeHunk[] = [];
  let hunk: ChangeHunk | null = null;
  let oldNo = 0;
  let newNo = 0;

  for (const raw of diff) {
    const head = HUNK.exec(raw);
    if (head) {
      oldNo = Number(head[1]);
      newNo = Number(head[3]);
      hunk = {
        oldStart: oldNo,
        oldCount: head[2] === undefined ? 1 : Number(head[2]),
        newStart: newNo,
        newCount: head[4] === undefined ? 1 : Number(head[4]),
        lines: [],
      };
      hunks.push(hunk);
      continue;
    }
    if (!hunk) continue;
    const marker = raw.charAt(0);
    const text = raw.slice(1);
    switch (marker) {
      case ' ':
      case '':
        hunk.lines.push({ kind: 'context', oldNo: oldNo++, newNo: newNo++, text });
        break;
      case '-':
        hunk.lines.push({ kind: 'remove', oldNo: oldNo++, newNo: null, text });
        break;
      case '+':
        hunk.lines.push({ kind: 'add', oldNo: null, newNo: newNo++, text });
        break;
      // "\ No newline at end of file" and anything else a unified diff may carry: not a line of
      // the body, so it does not get a row.
      default:
        break;
    }
  }
  for (const h of hunks) emphasize(h.lines);
  return hunks;
}

/** emphasize pairs the removed and added lines of each change block and marks the words that
 *  actually differ. A block whose sides have different sizes is left as a tinted line: pairing by
 *  position would invent a relationship the diff does not have. */
function emphasize(lines: ChangeLine[]): void {
  for (let i = 0; i < lines.length; ) {
    if (lines[i].kind === 'context') {
      i++;
      continue;
    }
    const start = i;
    while (i < lines.length && lines[i].kind !== 'context') i++;
    const block = lines.slice(start, i);
    const removes = block.filter((line) => line.kind === 'remove');
    const adds = block.filter((line) => line.kind === 'add');
    if (removes.length !== adds.length || removes.length === 0) continue;
    for (let k = 0; k < removes.length; k++) {
      const [before, after] = wordSegments(removes[k].text, adds[k].text);
      removes[k].segments = before;
      adds[k].segments = after;
    }
  }
}

/** TOKEN keeps words together (an apostrophe or a hyphen inside one is part of it), separates
 *  whitespace, and gives punctuation a token of its own. */
const TOKEN = /[\p{L}\p{N}]+(?:['’\-][\p{L}\p{N}]+)*|\s+|[^\p{L}\p{N}\s]+/gu;

/** wordSegments marks the tokens that differ between two lines. The shared head and tail stay
 *  plain; the middle — where a word was replaced, inserted or removed — is emphasized. */
export function wordSegments(before: string, after: string): [ChangeSegment[], ChangeSegment[]] {
  const a = before.match(TOKEN) ?? [];
  const b = after.match(TOKEN) ?? [];
  let prefix = 0;
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) prefix++;
  let suffix = 0;
  while (
    suffix < a.length - prefix &&
    suffix < b.length - prefix &&
    a[a.length - 1 - suffix] === b[b.length - 1 - suffix]
  ) {
    suffix++;
  }
  const [aStart, aEnd] = changedRange(a, prefix, a.length - suffix);
  const [bStart, bEnd] = changedRange(b, prefix, b.length - suffix);
  return [segmentsOf(a, aStart, aEnd), segmentsOf(b, bStart, bEnd)];
}

/** changedRange keeps the emphasis on words, not on the space that made room for one: a trailing
 *  blank belongs to the unchanged text on both sides, so "alpha new beta" marks only "new". */
function changedRange(tokens: string[], start: number, end: number): [number, number] {
  while (start < end && /^\s+$/.test(tokens[start])) start++;
  while (end > start && /^\s+$/.test(tokens[end - 1])) end--;
  return [start, end];
}

/** segmentsOf joins neighbouring tokens with the same emphasis into one segment: fewer nodes, and
 *  one <mark> around a changed phrase instead of one per word. */
function segmentsOf(tokens: string[], start: number, end: number): ChangeSegment[] {
  const out: ChangeSegment[] = [];
  for (let i = 0; i < tokens.length; i++) {
    const changed = i >= start && i < end;
    const last = out[out.length - 1];
    if (last && last.changed === changed) last.text += tokens[i];
    else out.push({ text: tokens[i], changed });
  }
  return out;
}

/** changeLineCounts is the summary the strip shows: whole-change counts, and whether the body
 *  moved at all. */
export function changeLineCounts(record: ChangeRecord | null): { added: number; removed: number } | null {
  if (!record || record.body !== 'changed') return null;
  return { added: record.added, removed: record.removed };
}
