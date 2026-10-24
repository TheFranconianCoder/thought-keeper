<script lang="ts">
  import {
    cleanupClusters,
    clusterClaims,
    collapseDuplicateClaims,
    curateVocabulary,
    fixURLs,
    migrateURLAddresses,
    rebuildAllTrees,
    reconcileVault,
    refreshURLs,
    reindexVault,
    reorganizeClaims,
    retopicClaims,
    runLint,
  } from '../../lib/actions';
  import type { Finding } from '../../lib/api/types';
  import { elapsed, plural, relTime } from '../../lib/format';
  import { claimPath, navigate, router, sourcePath } from '../../lib/router.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { jobStepLabel } from '../../lib/source-state';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';

  const jobs = $derived(system.pipeline?.jobs ?? []);
  const actionable = $derived(system.findings.filter((finding) => finding.severity !== 'info'));
  const informational = $derived(system.findings.filter((finding) => finding.severity === 'info'));
  const indexOutdated = $derived(system.status?.index.outdated ?? '');

  let clock = $state(Date.now());

  $effect(() => {
    const list = system.pipeline?.jobs ?? [];
    if (!list.some((job) => job.since)) return;
    const timer = setInterval(() => (clock = Date.now()), 1000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (router.current.view !== 'status') return;
    void system.loadStatus();
    void system.loadFindings();
    if (!system.vaultLoaded) void system.loadVault();
  });

  const jobRows = $derived.by(() => {
    const head = jobs.filter((job) => job.state === 'running' || job.state === 'waiting');
    const queued = jobs.filter((job) => job.state === 'queued' || job.state === 'pending');
    const tail = jobs.filter(
      (job) =>
        job.state !== 'running' &&
        job.state !== 'waiting' &&
        job.state !== 'queued' &&
        job.state !== 'pending',
    );
    return {
      rows: [...head, ...queued.slice(0, 8), ...tail],
      hidden: Math.max(0, queued.length - 8),
    };
  });

  const busy = (job: string): boolean =>
    (system.pipeline?.jobs ?? []).some(
      (entry) => entry.step === job && (entry.state === 'running' || entry.state === 'queued' || entry.state === 'waiting'),
    );

  const caps = $derived.by(() => {
    const status = system.status;
    if (!status) return [] as { label: string; ok: boolean; text: string; why?: string }[];
    return [
      { label: 'PDF', ok: status.tools.pdf, text: status.tools.pdf ? 'ready' : 'missing' },
      { label: 'OCR', ok: status.tools.ocr, text: status.tools.ocr ? 'ready' : 'missing' },
      { label: 'Agent', ok: status.agent.configured, text: status.agent.configured ? status.agent.model || 'ready' : 'not configured' },
      { label: 'Vault', ok: status.vault.writable, text: status.vault.writable ? 'writable' : 'read-only' },
      { label: 'Search', ok: status.index.search === 'fts5', text: status.index.search },
      {
        label: 'Inbox',
        ok: status.app.inbox?.watched ?? true,
        text: status.app.inbox?.watched === false ? 'polled' : 'watched',
        why: status.app.inbox?.error ? `Polled instead of watched — ${status.app.inbox.error}` : undefined,
      },
    ];
  });

  function liveOf(hash?: string): string {
    if (!hash) return '';
    const live = sources.progress[hash];
    if (!live) return '';
    const done = live.fields.parts_done;
    const total = live.fields.parts_total;
    const parts = typeof done === 'number' && total ? ` · ${done}/${total} parts` : '';
    if (live.phase === 'queued') return live.msg || 'waiting';
    if (live.msg) return `${live.msg}${parts}`;
    if (live.phase) return `${live.phase}${parts}`;
    return parts.replace(' · ', '');
  }

  function openFinding(finding: Finding): void {
    if (finding.subject_kind === 'atom' && finding.subject_id) navigate(claimPath(finding.subject_id));
    else if (finding.subject_kind === 'source' && finding.subject_id) navigate(sourcePath(finding.subject_id));
  }

  const clusterActions = [
    {
      label: 'Cluster claims',
      job: 'cluster',
      hint: 'Step 4 by hand: group the claims no cluster owns yet. It waits until no document step is running.',
      run: clusterClaims,
    },
  ];

  const vaultActions = [
    {
      label: 'Check consistency',
      job: 'reconcile',
      hint: 'Walk the vault and verify that every step record matches what is on disk, and that the index mirrors the records — a source that lost its owner is restored from its record (no source is derived again). A missing outline invalidates its derivation.',
      run: reconcileVault,
    },
    {
      label: 'Reindex',
      job: 'reindex',
      hint: 'Rebuild the index from the vault: sources, claims and clusters; drop rows whose file is gone.',
      run: reindexVault,
    },
    {
      label: 'Rebuild outlines',
      job: 'rebuild-trees',
      hint: 'Rebuild every source’s outline (step 2) from its capture — no extraction, no model.',
      run: rebuildAllTrees,
    },
    {
      label: 'Fix URLs',
      job: 'fix-urls',
      hint: 'Repair sources that lost the fact that they are URL snapshots. Their frontmatter still names the address.',
      run: fixURLs,
    },
    {
      label: 'Refresh URL sources',
      job: 'refresh-urls',
      hint: 'Re-fetch every known URL once. A changed page is corrected through its existing source.',
      run: refreshURLs,
    },
    {
      label: 'Collapse duplicate claims',
      job: 'collapse-atoms',
      hint: 'Keep one copy of every claim that exists more than once and move the indistinguishable copies to the trash.',
      run: collapseDuplicateClaims,
    },
    {
      label: 'Move URL sources to their URL address',
      job: 'migrate-urls',
      hint: 'A URL source is addressed by its URL now. This is a rename; nothing is derived again.',
      run: migrateURLAddresses,
    },
    {
      label: 'Reorganize claims',
      job: 'reorganize-atoms',
      hint: 'Move claims out of legacy topic folders to atoms/<ab>/<cd>/<id>.md and remove the folders that empty out.',
      run: reorganizeClaims,
    },
  ];
</script>

<div class="page status-page">
  <div class="page-head">
    <div>
      <h1>Status</h1>
      <p class="sub">
        {#if system.status}
          Thought Keeper {system.status.version} · up {system.status.app.uptime} · {system.status.app.addr}
        {:else}
          The vault, the index and the work in flight.
        {/if}
      </p>
    </div>
    <span class="spacer"></span>
    {#if system.status}
      <span class="chip chip--mono">PID {system.status.app.pid}</span>
    {/if}
  </div>

  {#if !system.status}
    <EmptyState icon="status" title="Nothing loaded" text="Thought Keeper is not reachable. Start the app and this page reconnects by itself." />
  {:else}
    {@const status = system.status}
    {#if indexOutdated}
      <div class="notice notice--error index-warning">
        <Icon name="warning" size={15} />
        <span><b>The index has another shape.</b> {indexOutdated}</span>
      </div>
    {/if}

    <div class="stat-grid">
      <div class="stat"><span class="stat-num">{status.index.sources}</span><span class="stat-label">sources</span></div>
      <div class="stat"><span class="stat-num">{status.index.atoms}</span><span class="stat-label">claims</span></div>
      <div class="stat"><span class="stat-num">{status.index.clusters}</span><span class="stat-label">clusters</span></div>
      <div class="stat"><span class="stat-num">{status.index.unclustered}</span><span class="stat-label">unclustered</span></div>
      <div class="stat"><span class="stat-num">{status.index.runs}</span><span class="stat-label">runs</span></div>
    </div>

    <div class="caps">
      {#each caps as cap (cap.label)}
        <span class="cap" class:ok={cap.ok} title={cap.why ?? ''}>
          <span class="cap-dot"></span>
          <span class="cap-label">{cap.label}</span>
          <span class="cap-text">{cap.text}</span>
        </span>
      {/each}
    </div>

    <section class="section">
      <div class="section-head">
        <h2>Job queue</h2>
        <span class="count">{jobs.length}</span>
      </div>
      {#if jobs.length === 0}
        <p class="muted small">Nothing is running — no document step, no cluster pass, no ask.</p>
      {:else}
        <div class="job-list">
          {#each jobRows.rows as job (job.step + job.state + (job.target ?? ''))}
            <div class="job-row">
              <span class="job-step">{jobStepLabel(job.step)}</span>
              <span class="job-state job-state--{job.state}" title={job.why ?? ''}>
                {job.state === 'pending' ? 'owed' : job.state}
              </span>
              {#if job.target}
                <button class="job-target" onclick={() => navigate(sourcePath(job.target!))}>
                  <Icon name="sources" size={12} />
                  {job.title || job.target.slice(0, 12)}
                </button>
              {:else}
                <span class="muted small">over the whole vault</span>
              {/if}
              {#if job.detail}<span class="job-live">{job.detail}</span>{/if}
              {#if liveOf(job.target)}<span class="job-live">{liveOf(job.target)}</span>{/if}
              {#if job.why && !liveOf(job.target)}<span class="job-live">{job.why}</span>{/if}
              <span class="grow"></span>
              {#if job.since}<span class="job-time" title={job.since}>{elapsed(job.since, clock)}</span>{/if}
            </div>
          {/each}
          {#if jobRows.hidden > 0}
            <div class="job-row job-row--more">
              <span class="muted small">{jobRows.hidden} more queued — they run as the workers reach them</span>
            </div>
          {/if}
        </div>
      {/if}
    </section>

    {#if status.summary}
      <section class="section">
        <div class="section-head">
          <h2>Summaries</h2>
          {#if status.summary.at}
            <span class="muted small">checked {relTime(status.summary.at)}</span>
          {/if}
        </div>
        <div class="stat-grid stat-grid--three">
          <div class="stat"><span class="stat-num">{status.summary.missing}</span><span class="stat-label">missing</span></div>
          <div class="stat"><span class="stat-num">{status.summary.queued}</span><span class="stat-label">queued</span></div>
          <div class="stat"><span class="stat-num">{status.summary.running}</span><span class="stat-label">running</span></div>
        </div>
        <p class="muted small section-note">
          A source's overview is written after a complete distill. The consistency check measures the
          missing ones; the summary worker fills them in a few at a time, so this drains in the background.
        </p>
      </section>
    {/if}

    <section class="section">
      <div class="section-head"><h2>Clusters</h2></div>
      <div class="action-list">
        {#each clusterActions as action (action.label)}
          <div class="action-row">
            <button class="btn btn--secondary" disabled={busy(action.job)} onclick={() => void action.run()}>
              {action.label}
            </button>
            <span class="action-hint">{action.hint}</span>
          </div>
        {/each}
      </div>
    </section>

    <section class="section">
      <div class="section-head">
        <h2>Vocabulary</h2>
        <span class="grow"></span>
        <button class="btn btn--primary btn--sm" disabled={busy('vocabulary')} onclick={() => void curateVocabulary()}>
          <Icon name="spark" size={13} /> Curate
        </button>
        <button class="btn btn--secondary btn--sm" disabled={busy('vocabulary')} onclick={() => void retopicClaims()}>
          <Icon name="distill" size={13} /> Re-topic claims
        </button>
      </div>
      <p class="muted small section-note">
        <b>Curate</b> asks the model what the names should become — claim topics, wiki tags and source
        tags — and applies the answer as a rename. A claim keeps its text, its node and its
        associations; a page and a source keep their prose. Only the label changes. Topics are flat.
      </p>
      {#if system.vocabulary && system.vocabulary.length > 0}
        <div class="vocab-list">
          {#each system.vocabulary as vocab (vocab.namespace)}
            <div class="vocab-row">
              <span class="vocab-name">{vocab.namespace}</span>
              <span class="vocab-numbers">
                {vocab.names} names · {vocab.uses} uses · {vocab.singletons} once
                ({Math.round(vocab.singleton_share * 100)}%) · top {Math.round(vocab.coverage * 100)}%
                {#if vocab.canonical || vocab.absorbed}· {vocab.canonical} canonical, {vocab.absorbed} absorbed{/if}
                {#if vocab.new?.length}· <b>{vocab.new.length} new</b>{/if}
              </span>
              <span class="vocab-flags">
                {#if vocab.collisions?.length}
                  <span class="chip chip--warn" title="Two spellings of one name — always a bug">
                    {vocab.collision_count ?? vocab.collisions.length} collision(s)
                  </span>
                {/if}
                {#if vocab.near_duplicates?.length}
                  <span class="chip" title="Names that share their words — a question worth asking">
                    {vocab.near_duplicate_count ?? vocab.near_duplicates.length} shared-word families
                  </span>
                {/if}
                {#if vocab.broad_tokens}
                  <span class="chip" title="Learned from this vault: ignored by near-duplicate detection">
                    {vocab.broad_tokens} broad token(s)
                  </span>
                {/if}
                {#if vocab.oversized_families}
                  <span class="chip" title="Too broad to merge: counted, never suggested">
                    {vocab.oversized_families} broad families ignored
                  </span>
                {/if}
              </span>
            </div>
          {/each}
        </div>
      {:else}
        <p class="muted small">No vocabulary report yet.</p>
      {/if}
    </section>

    <section class="section">
      <div class="section-head"><h2>Vault</h2></div>
      {#if system.duplicates && system.duplicates.groups > 0}
        <p class="muted small section-note">
          {system.duplicates.files} claim file(s) in {plural(system.duplicates.groups, 'group')} exist more than once{#if system.duplicates.example}
            — e.g. “{system.duplicates.example}”{/if}. The copies are indistinguishable; collapsing keeps one.
        </p>
      {/if}
      {#if system.layout && (system.layout.folders?.length ?? 0) > 0}
        <p class="muted small section-note">
          {system.layout.claims} claim(s) are still in the legacy folder layout
          ({plural(system.layout.folders?.length ?? 0, 'legacy folder')}
          {#if system.layout.folders?.length}: {system.layout.folders.slice(0, 6).join(', ')}{#if system.layout.folders.length > 6}, …{/if}{/if}).
          Claims are addressed by their ID now; moving them changes only the file names.
        </p>
      {/if}
      <div class="action-list">
        {#each vaultActions as action (action.label)}
          <div class="action-row">
            <button class="btn btn--secondary" disabled={busy(action.job)} onclick={() => void action.run()}>
              {action.label}
            </button>
            <span class="action-hint">{action.hint}</span>
          </div>
        {/each}
      </div>
    </section>

    <section class="section">
      <div class="section-head">
        <h2>Knowledge health</h2>
        <span class="grow"></span>
        <button class="btn btn--primary btn--sm" disabled={busy('lint')} onclick={() => void runLint()}>
          <Icon name="search" size={13} /> Run lint
        </button>
        <button class="btn btn--secondary btn--sm" disabled={busy('cleanup')} onclick={() => void cleanupClusters()}>
          <Icon name="spark" size={13} /> Cleanup
        </button>
      </div>
      {#if system.notice}<p class="muted small section-note">{system.notice}</p>{/if}
      {#if actionable.length === 0 && informational.length === 0}
        <p class="muted small">No findings.</p>
      {:else}
        <div class="findings">
          {#each actionable as finding (finding.id)}
            <div class="finding">
              <span class="chip {finding.severity === 'error' ? 'chip--danger' : 'chip--warn'}">{finding.severity}</span>
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
          {#if informational.length > 0}
            <details class="info-findings">
              <summary>{informational.length} informational finding{informational.length === 1 ? '' : 's'}</summary>
              {#each informational as finding (finding.id)}
                <div class="finding">
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
            </details>
          {/if}
        </div>
      {/if}
    </section>

    <section class="section details-section">
      <div class="section-head"><h2>Vault details</h2></div>
      <div class="details-grid">
        <div class="kv"><span class="k">vault</span><span class="v mono">{status.vault.root}</span></div>
        <div class="kv"><span class="k">index</span><span class="v mono">{status.index.path}</span></div>
        <div class="kv"><span class="k">sources</span><span class="v mono">{status.vault.sources}</span></div>
        <div class="kv"><span class="k">inbox</span><span class="v mono">{status.vault.inbox}</span></div>
        <div class="kv"><span class="k">started</span><span class="v">{relTime(status.app.started_at)}</span></div>
        <div class="kv"><span class="k">agent</span><span class="v">{status.agent.model || 'not configured'}</span></div>
        {#if status.memory}
          <div class="kv">
            <span class="k">memory</span>
            <span class="v">
              heap {status.memory.heap_mb} MB · peak {status.memory.heap_peak_mb} MB
              {#if status.memory.peak_work}<em>{status.memory.peak_work}</em>{/if}
            </span>
          </div>
        {/if}
        <div class="kv"><span class="k">pdf</span><span class="v">{status.tools.text_tool || '—'}</span></div>
        <div class="kv"><span class="k">ocr</span><span class="v">{status.tools.tesseract || '—'}</span></div>
      </div>
    </section>
  {/if}
</div>

<style>
  .status-page {
    padding-top: 24px;
  }

  .index-warning {
    margin-bottom: 18px;
    align-items: center;
  }

  .stat-grid {
    display: grid;
    grid-template-columns: repeat(5, minmax(0, 1fr));
    gap: 8px;
    margin-bottom: 16px;
  }

  .stat-grid--three {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }

  .stat {
    display: flex;
    flex-direction: column;
    gap: 1px;
    padding: 11px 13px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
  }

  .stat-num {
    color: var(--ink);
    font-size: 20px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    letter-spacing: -0.02em;
  }

  .stat-label {
    color: var(--faint);
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .caps {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    margin-bottom: 26px;
  }

  .cap {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 23px;
    padding: 0 8px;
    border: 1px solid var(--line);
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
    color: var(--muted);
    font-size: 11px;
  }

  .cap-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--danger);
  }

  .cap.ok .cap-dot {
    background: var(--success);
  }

  .cap-label {
    color: var(--ink-2);
    font-weight: 550;
  }

  .cap-text {
    color: var(--faint);
  }

  .job-list {
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    overflow: hidden;
  }

  .job-row {
    display: flex;
    align-items: center;
    gap: 9px;
    min-height: 35px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--line);
    font-size: 12px;
  }

  .job-row:last-child {
    border-bottom: 0;
  }

  .job-row--more {
    color: var(--faint);
  }

  .job-step {
    width: 105px;
    flex: none;
    color: var(--ink-2);
    font-weight: 550;
  }

  .job-state {
    flex: none;
    padding: 1px 6px;
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
    color: var(--faint);
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .job-state--running {
    background: var(--info-soft);
    color: var(--info);
  }

  .job-state--waiting {
    background: var(--warning-soft);
    color: var(--warning);
  }

  .job-target {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 260px;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--ink-2);
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .job-target:hover {
    color: var(--accent);
  }

  .job-live {
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .job-time {
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10.5px;
  }

  .action-list {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    overflow: hidden;
  }

  .action-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 8px 10px;
    border-bottom: 1px solid var(--line);
  }

  .action-row:last-child {
    border-bottom: 0;
  }

  .action-row .btn {
    width: 210px;
    flex: none;
    justify-content: flex-start;
  }

  .action-hint {
    color: var(--faint);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .section-note {
    margin: -2px 0 10px;
    line-height: 1.55;
  }

  .vocab-list {
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    overflow: hidden;
  }

  .vocab-row {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 9px 11px;
    border-bottom: 1px solid var(--line);
    font-size: 11.5px;
  }

  .vocab-row:last-child {
    border-bottom: 0;
  }

  .vocab-name {
    width: 90px;
    flex: none;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 11px;
  }

  .vocab-numbers {
    flex: 1;
    min-width: 0;
    color: var(--muted);
  }

  .vocab-flags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .findings {
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    overflow: hidden;
  }

  .finding {
    display: flex;
    align-items: flex-start;
    gap: 9px;
    padding: 9px 11px;
    border-bottom: 1px solid var(--line);
  }

  .finding:last-child {
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

  .info-findings summary {
    padding: 9px 11px;
    color: var(--muted);
    font-size: 11.5px;
    cursor: pointer;
    border-bottom: 1px solid var(--line);
  }

  .details-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0 24px;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
  }

  .details-grid .kv {
    align-items: flex-start;
  }

  .details-grid .kv em {
    color: var(--text-muted, #7a756d);
    font-style: normal;
    font-size: 0.86em;
  }

  .details-grid .v {
    word-break: break-all;
  }

  @media (max-width: 860px) {
    .stat-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .action-row {
      align-items: flex-start;
      flex-direction: column;
      gap: 5px;
    }

    .action-row .btn {
      width: auto;
    }

    .details-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  @media (max-width: 560px) {
    .stat-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .vocab-row {
      flex-direction: column;
      align-items: flex-start;
      gap: 4px;
    }

    .job-step {
      width: 78px;
    }
  }
</style>
