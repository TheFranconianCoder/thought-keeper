<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { navigate, claimPath, router, sourcePath } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { failedWindow } from '../../lib/stores/source-window.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import type { Finding, Source } from '../../lib/api/types';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { statusLabel } from '../../lib/source-state';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import ListHead from '../ui/ListHead.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceWindowList from '../sources/SourceWindowList.svelte';

  // The source half is a WINDOW over the failed steps: the server filters, the count is the whole
  // failed list. Findings are a separate, small list from the lint pass. One search narrows both.
  let query = $state('');
  const term = $derived(query.trim().toLowerCase());
  const searching = $derived(term.length > 0);

  const allActionable = $derived(system.findings.filter((finding) => finding.severity !== 'info'));
  const allInformational = $derived(system.findings.filter((finding) => finding.severity === 'info'));
  const actionable = $derived(allActionable.filter((finding) => matches(finding, term)));
  const informational = $derived(allInformational.filter((finding) => matches(finding, term)));
  const failedCount = $derived(failedWindow.count);
  // The head counts what the page holds, not what the current search happens to show: the unfiltered
  // failed total is remembered while no search is active.
  let failedTotal = $state(0);
  const total = $derived(failedTotal + allActionable.length);
  // Both halves have to be here before the page may say "nothing needs attention": an empty list
  // while the sources are still loading is not a finding.
  const ready = $derived(failedWindow.loaded && system.findingsLoaded);

  failedWindow.setFilter({ failedOnly: true });

  $effect(() => {
    if (!searching && failedWindow.loaded) failedTotal = failedWindow.count;
  });

  let filterTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const next = term;
    clearTimeout(filterTimer);
    filterTimer = setTimeout(() => {
      failedWindow.setFilter({ failedOnly: true, query: next });
    }, 100);
    return () => clearTimeout(filterTimer);
  });

  $effect(() => {
    if (router.current.view !== 'attention') return;
    void system.loadFindings();
  });

  $effect(() => {
    // A reset discards the pages. With a count, the list asks for the visible range itself; only an
    // empty/not-yet-known list needs the first page here.
    void failedWindow.epoch;
    if (!failedWindow.loaded || failedWindow.count === 0) {
      void failedWindow.ensure(0, 30);
    }
  });

  function matches(finding: Finding, text: string): boolean {
    if (!text) return true;
    return (
      finding.kind.toLowerCase().includes(text) || finding.message.toLowerCase().includes(text)
    );
  }

  function retry(): void {
    failedWindow.reset();
    void system.loadFindings();
  }

  function openFinding(finding: Finding): void {
    if (finding.subject_kind === 'atom' && finding.subject_id) navigate(claimPath(finding.subject_id));
    else if (finding.subject_kind === 'source' && finding.subject_id) navigate(sourcePath(finding.subject_id));
  }
</script>

<div class="page attention-page">
  <ListHead title={t('view.attention')} count={ready ? total : null}>
    {#snippet action()}
      <button class="btn btn--secondary" onclick={() => navigate('#/status')}>
        <Icon name="status" size={14} /> <span class="btn-text">{t('attention.vaultStatus')}</span>
      </button>
    {/snippet}

    <div class="search-wrap list-search">
      <Icon name="search" size={14} />
      <input
        use:clearable
        class="input input--search"
        placeholder={t('attention.filterPlaceholder')}
        bind:value={query}
        aria-label={t('attention.filterAria')}
      />
    </div>
  </ListHead>

  {#if !ready}
    {#if connection.state !== 'live'}
      <EmptyState icon="attention" title={t('attention.unreachableTitle')} text={t('attention.unreachableText')} />
    {:else}
      <div class="stack">
        {#each Array(3) as _, index (index)}<span class="skeleton" style="height:52px"></span>{/each}
      </div>
    {/if}
  {:else}
    {#if failedWindow.error}
      <p class="notice notice--error">
        {t('attention.sourcesFailed', { error: failedWindow.error })}
        <button class="btn btn--ghost btn--sm" onclick={retry}>{t('common.retry')}</button>
      </p>
    {/if}
    {#if failedCount > 0}
      <section class="section">
        <div class="section-head">
          <h2>{t('attention.failedSteps')}</h2>
          <span class="count">{n(failedCount)}</span>
        </div>
        <div class="attention-list">
          <SourceWindowList
            count={failedWindow.count}
            rowHeight={48}
            itemAt={(index) => failedWindow.itemAt(index)}
            ensure={(start, end) => void failedWindow.ensure(start, end)}
            epoch={failedWindow.epoch}
          >
            {#snippet row(source: Source | undefined)}
              {#if source}
                {@const live = sources.progress[source.hash]}
                <button class="attention-row" onclick={() => navigate(sourcePath(source.hash))}>
                  <SourceIcon kind={source.kind} hash={source.hash} size={16} />
                  <span class="attention-main">
                    <span class="attention-title">{source.title || source.hash.slice(0, 12)}</span>
                    <span class="attention-sub">{statusLabel(source, live)}</span>
                  </span>
                  <span class="chip chip--danger">{t('attention.failedChip')}</span>
                  <Icon name="chevronRight" size={14} />
                </button>
              {:else}
                <div class="attention-row attention-row--skeleton" aria-hidden="true">
                  <span class="skeleton attention-skeleton"></span>
                </div>
              {/if}
            {/snippet}
          </SourceWindowList>
        </div>
      </section>
    {/if}

    {#if actionable.length > 0}
      <section class="section">
        <div class="section-head">
          <h2>{t('attention.lintFindings')}</h2>
          <span class="count">{n(actionable.length)}</span>
        </div>
        <div class="attention-list">
          {#each actionable as finding (finding.id)}
            <div class="finding-row">
              <span class="chip {finding.severity === 'error' ? 'chip--danger' : 'chip--warn'}">
                {finding.severity}
              </span>
              <div class="finding-main">
                <div class="finding-kind">{finding.kind}</div>
                <div class="finding-message" title={finding.message}>{finding.message}</div>
              </div>
              {#if finding.subject_id && (finding.subject_kind === 'atom' || finding.subject_kind === 'source')}
                <button class="btn btn--secondary btn--sm" onclick={() => openFinding(finding)}>
                  {t('attention.open', { kind: finding.subject_kind })}
                </button>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}

    {#if informational.length > 0}
      <details class="info-findings">
        <summary>{tn('attention.infoCount', informational.length)}</summary>
        <div class="attention-list">
          {#each informational as finding (finding.id)}
            <div class="finding-row">
              <span class="chip">{finding.severity}</span>
              <div class="finding-main">
                <div class="finding-kind">{finding.kind}</div>
                <div class="finding-message" title={finding.message}>{finding.message}</div>
              </div>
              {#if finding.subject_id && (finding.subject_kind === 'atom' || finding.subject_kind === 'source')}
                <button class="btn btn--ghost btn--sm" onclick={() => openFinding(finding)}>
                  {t('attention.open', { kind: finding.subject_kind })}
                </button>
              {/if}
            </div>
          {/each}
        </div>
      </details>
    {/if}

    {#if system.findingsError}
      <p class="notice notice--error">{t('attention.findingsFailed', { error: system.findingsError })}</p>
    {:else if searching && !failedWindow.loading && failedWindow.count === 0 && actionable.length === 0 && informational.length === 0}
      <EmptyState
        icon="filter"
        title={t('attention.noMatchTitle')}
        text={t('attention.noMatchText')}
      />
    {:else if total === 0}
      <EmptyState
        icon="check"
        title={t('attention.emptyTitle')}
        text={t('attention.emptyText')}
      />
    {/if}
  {/if}
</div>

<style>
  .attention-page {
    padding-top: 24px;
  }

  .attention-list {
    border: 1px solid var(--line);
    border-radius: var(--radius);
    overflow: hidden;
    background: var(--bg-elevated);
  }

  .attention-row {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 48px;
    padding: 8px 11px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: inherit;
    text-align: left;
  }

  .attention-row:last-child {
    border-bottom: 0;
  }

  .attention-row:hover {
    background: var(--bg-hover);
  }

  .attention-row--skeleton {
    padding: 0 11px;
  }

  .attention-row--skeleton:hover {
    background: transparent;
  }

  .attention-skeleton {
    width: 100%;
    height: 28px;
  }

  .attention-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .attention-title {
    overflow: hidden;
    color: var(--ink);
    font-size: 13px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .attention-sub {
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .finding-row {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 10px 11px;
    border-bottom: 1px solid var(--line);
  }

  .finding-row:last-child {
    border-bottom: 0;
  }

  .finding-main {
    flex: 1;
    min-width: 0;
  }

  .finding-kind {
    overflow: hidden;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .finding-message {
    overflow: hidden;
    color: var(--ink-2);
    font-size: 12.5px;
    line-height: 1.5;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .info-findings {
    margin-top: 22px;
  }

  .info-findings summary {
    margin-bottom: 8px;
    color: var(--muted);
    font-size: 12px;
    cursor: pointer;
  }
</style>
