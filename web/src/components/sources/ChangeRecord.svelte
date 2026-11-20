<script lang="ts">
  import type { ChangeHunk, ChangeRecord } from '../../lib/changes';
  import { dateTime, relTime } from '../../lib/format';
  import { n, t, tn } from '../../lib/i18n/index.svelte';

  /** The change report: what the last event of a URL page did, as a reader can see it.
   *
   *  The record is the truth (docs/storage.md); this component only renders it. A record that does
   *  not parse is shown as the raw file, never as half a diff. */
  interface Props {
    record: ChangeRecord | null;
    text: string;
  }

  const { record, text }: Props = $props();

  const titleMoved = $derived(!!record && record.titleBefore !== record.titleAfter);
  const lineTotal = $derived(record ? record.added + record.removed : 0);
  const addedShare = $derived(lineTotal > 0 ? (record!.added / lineTotal) * 100 : 0);

  /** hunkRange names the place of a hunk in the page the reader has now. A pure deletion has no
   *  new lines, so its old range is the one that exists. The range comes from the lines actually
   *  shown, not from the header's counts: a truncated record stops inside a hunk. */
  function hunkRange(hunk: ChangeHunk): string {
    const newNumbers = hunk.lines.flatMap((line) => (line.newNo === null ? [] : [line.newNo]));
    const oldNumbers = hunk.lines.flatMap((line) => (line.oldNo === null ? [] : [line.oldNo]));
    const numbers = newNumbers.length > 0 ? newNumbers : oldNumbers;
    const from = numbers[0] ?? 1;
    const to = numbers[numbers.length - 1] ?? from;
    return t('source.changeHunk', { from: String(from), to: String(to) });
  }
</script>

{#if record}
  <div class="change-report">
    <header class="change-head">
      <div class="change-when">
        <span class="change-ago">{relTime(record.at)}</span>
        <span class="change-at">{dateTime(record.at)}</span>
      </div>
      {#if record.body === 'changed' && lineTotal > 0}
        <div class="change-counts">
          {#if record.added > 0}
            <span
              class="change-count change-count--add"
              title={tn('source.changeAddedLines', record.added)}>+{n(record.added)}</span
            >
          {/if}
          {#if record.removed > 0}
            <span
              class="change-count change-count--remove"
              title={tn('source.changeRemovedLines', record.removed)}>−{n(record.removed)}</span
            >
          {/if}
        </div>
      {/if}
    </header>

    {#if titleMoved}
      <div class="change-title">
        <span class="change-label">{t('source.changeTitle')}</span>
        <div class="change-title-pair">
          <span class="change-title-old">{record.titleBefore || '—'}</span>
          <span class="change-arrow" aria-hidden="true">→</span>
          <span class="change-title-new">{record.titleAfter || '—'}</span>
        </div>
      </div>
    {/if}

    {#if record.body === 'changed' && lineTotal > 1}
      <!-- The proportion at a glance: how much of this change added text and how much removed it. -->
      <div class="change-bar" aria-hidden="true">
        <span class="change-bar-add" style="width:{addedShare}%"></span>
        <span class="change-bar-remove"></span>
      </div>
    {/if}

    {#if record.words !== null && record.avgChars !== null && record.words > 0}
      <p class="change-measure">
        {tn('source.changeWords', record.words, { avg: record.avgChars })}
      </p>
    {/if}

    {#if record.hunks.length > 0}
      <div class="diff" role="group" aria-label={t('source.changeDiffAria')}>
        {#each record.hunks as hunk, index (index)}
          <div class="diff-hunk">
            <span class="diff-hunk-label">{hunkRange(hunk)}</span>
          </div>
          {#each hunk.lines as line, lineIndex (lineIndex)}
            <div class="diff-line diff-line--{line.kind}">
              <span class="diff-no" aria-hidden="true">{line.oldNo ?? ''}</span>
              <span class="diff-no" aria-hidden="true">{line.newNo ?? ''}</span>
              <span class="diff-sign"
                >{line.kind === 'add' ? '+' : line.kind === 'remove' ? '−' : ''}</span
              >
              <code class="diff-text"
                >{#if line.segments && line.segments.length > 0}{#each line.segments as segment, segmentIndex (segmentIndex)}{#if segment.changed}<mark
                      class="diff-mark">{segment.text}</mark
                    >{:else}{segment.text}{/if}{/each}{:else}{line.text}{/if}</code
              >
            </div>
          {/each}
        {/each}
      </div>
    {:else if record.body === 'whitespace'}
      <p class="change-note">{t('source.changeWhitespace')}</p>
    {:else if record.body === 'unchanged'}
      <p class="change-note">{t('source.changeBodyUnchanged')}</p>
    {:else}
      <p class="change-note">{t('source.changeNoDiff')}</p>
    {/if}

    {#if record.truncated}
      <p class="change-note change-note--tail">{t('source.changeTruncated')}</p>
    {/if}

    <details class="change-raw">
      <summary>{t('source.changeRaw')}</summary>
      <pre class="change-raw-file">{text}</pre>
    </details>
  </div>
{:else}
  <p class="change-note">{t('source.changesIntro')}</p>
  <pre class="change-raw-file">{text}</pre>
{/if}

<style>
  .change-report {
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .change-head {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }

  .change-when {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .change-ago {
    color: var(--ink);
    font-size: 15px;
    font-weight: 600;
    letter-spacing: -0.01em;
  }

  .change-at {
    color: var(--faint);
    font-size: 11.5px;
  }

  .change-counts {
    display: flex;
    flex: none;
    gap: 6px;
  }

  .change-count {
    display: inline-flex;
    align-items: center;
    min-height: 24px;
    padding: 1px 9px;
    border-radius: var(--radius-pill);
    font-family: var(--font-mono);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }

  .change-count--add {
    background: var(--success-soft);
    color: var(--success);
  }

  .change-count--remove {
    background: var(--danger-soft);
    color: var(--danger);
  }

  .change-title {
    display: grid;
    grid-template-columns: 48px minmax(0, 1fr);
    gap: 10px;
    padding: 10px 0;
    border-top: 1px solid var(--line);
  }

  .change-label {
    padding-top: 3px;
    color: var(--faint);
    font-size: 10px;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .change-title-pair {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 6px;
    min-width: 0;
    font-size: 13px;
    line-height: 1.5;
  }

  .change-title-old {
    color: var(--danger);
    text-decoration: line-through;
    text-decoration-color: color-mix(in srgb, var(--danger) 45%, transparent);
  }

  .change-title-new {
    color: var(--success);
    font-weight: 600;
  }

  .change-arrow {
    color: var(--faint);
  }

  .change-bar {
    display: flex;
    height: 4px;
    margin: 2px 0 10px;
    overflow: hidden;
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
  }

  .change-bar-add {
    height: 100%;
    background: var(--success);
    opacity: 0.7;
  }

  .change-bar-remove {
    flex: 1;
    height: 100%;
    background: var(--danger);
    opacity: 0.65;
  }

  .change-measure {
    margin: 0 0 14px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.6;
  }

  .diff {
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg-elevated);
  }

  .diff-hunk {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px;
    padding: 5px 10px;
    border-top: 1px solid var(--line);
    background: var(--bg-muted);
    color: var(--muted);
    font-size: 11px;
  }

  .diff-hunk:first-child {
    border-top: 0;
  }

  .diff-hunk-label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .diff-line {
    display: grid;
    grid-template-columns: 3.6ch 3.6ch 1.6ch minmax(0, 1fr);
    align-items: baseline;
  }

  .diff-line--remove {
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    box-shadow: inset 2px 0 0 var(--danger);
  }

  .diff-line--add {
    background: color-mix(in srgb, var(--success) 13%, transparent);
    box-shadow: inset 2px 0 0 var(--success);
  }

  .diff-no {
    padding: 1px 6px 1px 2px;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    text-align: right;
    user-select: none;
  }

  .diff-sign {
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 11px;
    text-align: center;
    user-select: none;
  }

  .diff-line--remove .diff-sign {
    color: var(--danger);
  }

  .diff-line--add .diff-sign {
    color: var(--success);
  }

  .diff-text {
    margin: 0;
    padding: 1px 10px 1px 6px;
    overflow-wrap: anywhere;
    white-space: pre-wrap;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 11.5px;
    line-height: 1.65;
  }

  .diff-mark {
    padding: 0 1px;
    border-radius: 2px;
    color: inherit;
    background: color-mix(in srgb, var(--danger) 34%, transparent);
  }

  .diff-line--add .diff-mark {
    background: color-mix(in srgb, var(--success) 36%, transparent);
  }

  .change-note {
    margin: 0 0 14px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.6;
  }

  .change-note--tail {
    margin-top: 10px;
  }

  .change-raw {
    margin-top: 16px;
    padding-top: 10px;
    border-top: 1px solid var(--line);
  }

  .change-raw summary {
    color: var(--muted);
    font-size: 11.5px;
    cursor: pointer;
  }

  .change-raw[open] summary {
    margin-bottom: 10px;
  }

  .change-raw-file {
    margin: 0;
    overflow: auto;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.65;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
