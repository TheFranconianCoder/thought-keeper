<script lang="ts">
  import { navigate, claimPath, router, sourcePath } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import type { Finding } from '../../lib/api/types';
  import { statusLabel } from '../../lib/source-state';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';

  const failedSources = $derived(
    sources.items.filter((source) =>
      Object.values(source.steps ?? {}).some((step) => step.state === 'failed'),
    ),
  );
  const actionable = $derived(system.findings.filter((finding) => finding.severity !== 'info'));
  const informational = $derived(system.findings.filter((finding) => finding.severity === 'info'));
  const total = $derived(failedSources.length + actionable.length);
  // Both halves have to be here before the page may say "nothing needs attention": an empty list
  // while the sources are still loading is not a finding.
  const ready = $derived(sources.loaded && system.findingsLoaded);

  $effect(() => {
    if (router.current.view !== 'attention') return;
    void sources.loadList();
    void system.loadFindings();
  });

  function openFinding(finding: Finding): void {
    if (finding.subject_kind === 'atom' && finding.subject_id) navigate(claimPath(finding.subject_id));
    else if (finding.subject_kind === 'source' && finding.subject_id) navigate(sourcePath(finding.subject_id));
  }
</script>

<div class="page attention-page">
  <div class="page-head">
    <div>
      <h1>Attention</h1>
      <p class="sub">
        {#if !ready}
          Loading what needs attention…
        {:else if total === 0}
          Nothing needs a decision or a repair.
        {:else}
          {total} thing{total === 1 ? '' : 's'} need a decision or a repair.
        {/if}
      </p>
    </div>
    <span class="spacer"></span>
    <button class="btn btn--secondary" onclick={() => navigate('#/status')}>
      <Icon name="status" size={14} /> Vault status
    </button>
  </div>

  {#if !ready}
    {#if connection.state !== 'live'}
      <EmptyState icon="attention" title="Nothing loaded" text="Thought Keeper is not reachable, so this is not a statement about the vault." />
    {:else}
      <div class="stack">
        {#each Array(3) as _, index (index)}<span class="skeleton" style="height:52px"></span>{/each}
      </div>
    {/if}
  {:else}
    {#if sources.error}
      <p class="notice notice--error">
        {sources.error}
        <button class="btn btn--ghost btn--sm" onclick={() => void sources.loadList()}>Try again</button>
      </p>
    {/if}
    {#if failedSources.length > 0}
      <section class="section">
        <div class="section-head">
          <h2>Failed source steps</h2>
          <span class="count">{failedSources.length}</span>
        </div>
        <div class="attention-list">
          {#each failedSources as source (source.hash)}
            {@const live = sources.progress[source.hash]}
            <button class="attention-row" onclick={() => navigate(sourcePath(source.hash))}>
              <SourceIcon kind={source.kind} hash={source.hash} size={16} />
              <span class="attention-main">
                <span class="attention-title">{source.title || source.hash.slice(0, 12)}</span>
                <span class="attention-sub">{statusLabel(source, live)}</span>
              </span>
              <span class="chip chip--danger">failed</span>
              <Icon name="chevronRight" size={14} />
            </button>
          {/each}
        </div>
      </section>
    {/if}

    {#if actionable.length > 0}
      <section class="section">
        <div class="section-head">
          <h2>Lint findings</h2>
          <span class="count">{actionable.length}</span>
        </div>
        <div class="attention-list">
          {#each actionable as finding (finding.id)}
            <div class="finding-row">
              <span class="chip {finding.severity === 'error' ? 'chip--danger' : 'chip--warn'}">
                {finding.severity}
              </span>
              <div class="finding-main">
                <div class="finding-kind">{finding.kind}</div>
                <div class="finding-message">{finding.message}</div>
              </div>
              {#if finding.subject_id && (finding.subject_kind === 'atom' || finding.subject_kind === 'source')}
                <button class="btn btn--secondary btn--sm" onclick={() => openFinding(finding)}>
                  Open the {finding.subject_kind}
                </button>
            {/if}
          </div>
        {/each}
      </div>
    </section>
  {/if}

    {#if informational.length > 0}
      <details class="info-findings">
        <summary>{informational.length} informational finding{informational.length === 1 ? '' : 's'}</summary>
        <div class="attention-list">
          {#each informational as finding (finding.id)}
            <div class="finding-row">
              <span class="chip">{finding.severity}</span>
              <div class="finding-main">
                <div class="finding-kind">{finding.kind}</div>
                <div class="finding-message">{finding.message}</div>
              </div>
              {#if finding.subject_id && (finding.subject_kind === 'atom' || finding.subject_kind === 'source')}
                <button class="btn btn--ghost btn--sm" onclick={() => openFinding(finding)}>
                  Open the {finding.subject_kind}
                </button>
              {/if}
            </div>
          {/each}
        </div>
      </details>
    {/if}

    {#if system.findingsError}
      <p class="notice notice--error">{system.findingsError}</p>
    {:else if total === 0}
      <EmptyState
        icon="check"
        title="Nothing needs attention"
        text="Source derivations and lint findings are in order."
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
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .finding-message {
    color: var(--ink-2);
    font-size: 12.5px;
    line-height: 1.5;
    overflow-wrap: anywhere;
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
