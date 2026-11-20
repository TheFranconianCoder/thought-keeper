import { describe, expect, it } from 'vitest';
import { changeLineCounts, parseChangeRecord, wordSegments } from './changes';

const RECORD = `# Changed 2026-11-18T09:12:04Z

Title: "Cache invalidation, revisited" → "Cache invalidation, in practice"

Body: +1 -1 lines.
Words: 3 changed, 5.3 characters on average.

\`\`\`\`diff
@@ -3,3 +3,3 @@
 keep me
-the quick brown fox jumps
+the quick red fox jumps
 tail
\`\`\`\`
`;

describe('parseChangeRecord', () => {
  it('reads the summary, the title move and the numbered lines', () => {
    const record = parseChangeRecord(RECORD);
    expect(record).not.toBeNull();
    expect(record!.at).toBe('2026-11-18T09:12:04Z');
    expect(record!.titleBefore).toBe('Cache invalidation, revisited');
    expect(record!.titleAfter).toBe('Cache invalidation, in practice');
    expect(record!.added).toBe(1);
    expect(record!.removed).toBe(1);
    expect(record!.words).toBe(3);
    expect(record!.avgChars).toBe(5.3);
    expect(record!.body).toBe('changed');
    expect(record!.truncated).toBe(false);

    expect(record!.hunks).toHaveLength(1);
    const hunk = record!.hunks[0];
    expect(hunk.oldStart).toBe(3);
    expect(hunk.oldCount).toBe(3);
    expect(hunk.newStart).toBe(3);
    expect(hunk.newCount).toBe(3);
    expect(hunk.lines.map((line) => [line.kind, line.oldNo, line.newNo, line.text])).toEqual([
      ['context', 3, 3, 'keep me'],
      ['remove', 4, null, 'the quick brown fox jumps'],
      ['add', null, 4, 'the quick red fox jumps'],
      ['context', 5, 5, 'tail'],
    ]);
  });

  it('marks only the words that moved', () => {
    const line = parseChangeRecord(RECORD)!.hunks[0].lines[1];
    expect(line.segments).toEqual([
      { text: 'the quick ', changed: false },
      { text: 'brown', changed: true },
      { text: ' fox jumps', changed: false },
    ]);
    const added = parseChangeRecord(RECORD)!.hunks[0].lines[2];
    expect(added.segments).toEqual([
      { text: 'the quick ', changed: false },
      { text: 'red', changed: true },
      { text: ' fox jumps', changed: false },
    ]);
  });

  it('reads a title-only change as an unchanged body', () => {
    const record = parseChangeRecord(`# Changed 2026-11-18T09:12:04Z

Title: "Old" → "New"

Body: unchanged.
`);
    expect(record!.body).toBe('unchanged');
    expect(record!.hunks).toEqual([]);
    expect(record!.added).toBe(0);
    expect(record!.removed).toBe(0);
  });

  it('reads a whitespace-only change', () => {
    const record = parseChangeRecord(`# Changed 2026-11-18T09:12:04Z

Body: changed only in whitespace or line endings.
`);
    expect(record!.body).toBe('whitespace');
    expect(record!.hunks).toEqual([]);
  });

  it('keeps the truncation note and the counts it states', () => {
    const record = parseChangeRecord(`# Changed 2026-11-18T09:12:04Z

Body: +40 -2 lines.
Words: 12 changed, 4.5 characters on average.

\`\`\`\`diff
@@ -10,1 +10,2 @@
-old
+new
+more
\`\`\`\`

The diff stops early; the snapshot holds the full text.
`);
    expect(record!.truncated).toBe(true);
    // The record's own count covers the whole change, not just the rows that fit.
    expect(record!.added).toBe(40);
    expect(record!.removed).toBe(2);
  });

  it('counts hunks when the record carries no Body count', () => {
    const record = parseChangeRecord(`# Changed 2026-11-18T09:12:04Z

\`\`\`\`diff
@@ -1,1 +1,2 @@
-a
+b
+c
\`\`\`\`
`);
    expect(record!.added).toBe(2);
    expect(record!.removed).toBe(1);
  });

  it('refuses a file that is not a change record', () => {
    expect(parseChangeRecord('just a page')).toBeNull();
  });
});

describe('wordSegments', () => {
  it('marks an inserted word without touching its neighbours', () => {
    const [before, after] = wordSegments('alpha beta', 'alpha new beta');
    expect(before).toEqual([{ text: 'alpha beta', changed: false }]);
    expect(after).toEqual([
      { text: 'alpha ', changed: false },
      { text: 'new', changed: true },
      { text: ' beta', changed: false },
    ]);
  });

  it('leaves identical lines plain', () => {
    const [before, after] = wordSegments('same text', 'same text');
    expect(before).toEqual([{ text: 'same text', changed: false }]);
    expect(after).toEqual([{ text: 'same text', changed: false }]);
  });
});

describe('changeLineCounts', () => {
  it('is what the strip shows for a body change only', () => {
    expect(changeLineCounts(parseChangeRecord(RECORD))).toEqual({ added: 1, removed: 1 });
    expect(
      changeLineCounts(parseChangeRecord('# Changed 2026-11-18T09:12:04Z\n\nBody: unchanged.\n')),
    ).toBeNull();
    expect(changeLineCounts(null)).toBeNull();
  });
});
