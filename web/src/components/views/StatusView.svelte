<script lang="ts">
  import {
    clusterClaims,
    collapseDuplicateClaims,
    curateVocabulary,
    rebuildAllTrees,
    reconcileVault,
    refreshURLs,
    reindexVault,
    retopicClaims,
  } from '../../lib/actions';
  import { elapsed, relTime } from '../../lib/format';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { navigate, router, sourcePath } from '../../lib/router.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { jobStepLabel, repairPhaseLabel } from '../../lib/source-state';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';

  const jobs = $derived(system.pipeline?.jobs ?? []);
  const indexOutdated = $derived(system.status?.index.outdated ?? '');
  // The index rebuild runs before the vault accepts writes. The status read and the live pipeline
  // frames both carry it; the frames keep the bar moving.
  const repair = $derived(system.pipeline?.repair ?? system.status?.repair ?? null);
  const repairing = $derived(repair?.active === true);
  const repairPercent = $derived(
    repair && repair.total > 0 ? Math.min(100, Math.round((repair.done / repair.total) * 100)) : 0,
  );


  let clock = $state(Date.now());

  $effect(() => {
    const list = system.pipeline?.jobs ?? [];
    if (!list.some((job) => job.since)) return;
    const timer = setInterval(() => (clock = Date.now()), 1000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (router.current.view !== 'status') return;
    // Entering the view is its catch-up point: live events only refresh the vault report while this
    // view is visible, so a hidden Status page cannot keep re-reading duplicates and names.
    void system.loadStatus();
    void system.loadVault();
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
      { label: 'PDF', ok: status.tools.pdf, text: status.tools.pdf ? t('status.ready') : t('status.missing') },
      { label: 'OCR', ok: status.tools.ocr, text: status.tools.ocr ? t('status.ready') : t('status.missing') },
      {
        label: t('status.capAgent'),
        ok: status.agent.configured,
        text: status.agent.configured ? status.agent.model || t('status.ready') : t('status.notConfigured'),
      },
      { label: t('status.capVault'), ok: status.vault.writable, text: status.vault.writable ? t('status.writable') : t('status.readOnly') },
      { label: t('status.capSearch'), ok: status.index.search === 'fts5', text: status.index.search },
      {
        label: t('status.capInbox'),
        ok: status.app.inbox?.watched ?? true,
        text: status.app.inbox?.watched === false ? t('status.polled') : t('status.watched'),
        why: status.app.inbox?.error
          ? t('status.polledReason', { error: status.app.inbox.error })
          : undefined,
      },
    ];
  });

  function liveOf(hash?: string): string {
    if (!hash) return '';
    const live = sources.progress[hash];
    if (!live) return '';
    const done = live.fields.parts_done;
    const total = live.fields.parts_total;
    const parts = typeof done === 'number' && total ? tn('status.liveParts', total, { done, total }) : '';
    if (live.phase === 'queued') return live.msg || t('status.waiting');
    if (live.msg) return `${live.msg}${parts}`;
    if (live.phase) return `${live.phase}${parts}`;
    return parts.replace(' · ', '');
  }

  const clusterActions = $derived([
    {
      label: t('status.clusterClaims'),
      job: 'cluster',
      hint: t('status.clusterClaimsHint'),
      run: clusterClaims,
    },
  ]);

  const vaultActions = $derived([
    {
      label: t('status.reconcile'),
      job: 'reconcile',
      hint: t('status.reconcileHint'),
      run: reconcileVault,
    },
    {
      label: t('status.reindex'),
      job: 'reindex',
      hint: t('status.reindexHint'),
      run: reindexVault,
    },
    {
      label: t('status.rebuildOutlines'),
      job: 'rebuild-trees',
      hint: t('status.rebuildOutlinesHint'),
      run: rebuildAllTrees,
    },
    {
      label: t('status.refreshUrls'),
      job: 'refresh-urls',
      hint: t('status.refreshUrlsHint'),
      run: refreshURLs,
    },
  ]);

  // A duplicate claim is the only repair whose button waits for its own report: the writer prevents
  // one, so a vault that has none must not offer the action.
  const duplicates = $derived(system.duplicates);
</script>

<div class="page status-page">
  <div class="page-head">
    <div class="head-row">
      <h1>{t('view.status')}</h1>
    </div>
    <p class="sub">
      {#if system.status}
        {t('status.subLine', { version: system.status.version, uptime: system.status.app.uptime, addr: system.status.app.addr })}
      {:else}
        {t('status.sub')}
      {/if}
    </p>
  </div>

  {#if system.notice}
    <p class="notice page-notice">{system.notice}</p>
  {/if}

  {#if !system.status}
    <EmptyState icon="status" title={t('status.notLoaded')} text={t('status.notLoadedText')} />
  {:else}
    {@const status = system.status}
    {#if indexOutdated}
      <div class="notice notice--error index-warning">
        <Icon name="warning" size={15} />
        <span><b>{t('status.indexShape')}</b> {indexOutdated}</span>
      </div>
    {/if}

    {#if repairing}
      <section class="repair" role="status" aria-live="polite">
        <div class="repair-head">
          <Icon name="refresh" size={14} />
          <b>{t('status.repairTitle')}</b>
          <span class="muted small">{repairPhaseLabel(repair?.phase)}</span>
          <span class="grow"></span>
          {#if repair && repair.total > 0}
            <span class="repair-count">{n(repair.done)} / {n(repair.total)}</span>
          {/if}
        </div>
        <div class="repair-bar" aria-hidden="true"><span style="width: {repairPercent}%"></span></div>
        <p class="muted small section-note">{t('status.repairNote')}</p>
      </section>
    {/if}

    <div class="stat-grid">
      <div class="stat"><span class="stat-num">{n(status.index.sources)}</span><span class="stat-label">{t('status.statSources')}</span></div>
      <div class="stat"><span class="stat-num">{n(status.index.atoms)}</span><span class="stat-label">{t('status.statClaims')}</span></div>
      <div class="stat"><span class="stat-num">{n(status.index.clusters)}</span><span class="stat-label">{t('status.statClusters')}</span></div>
      <div class="stat"><span class="stat-num">{n(status.index.unclustered)}</span><span class="stat-label">{t('status.statUnclustered')}</span></div>
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
        <h2>{t('status.jobQueue')}</h2>
        <span class="count">{n(jobs.length)}</span>
      </div>
      {#if jobs.length === 0}
        <p class="muted small">{t('status.noJobs')}</p>
      {:else}
        <div class="job-list">
          <!-- Keyed by POSITION: several jobs share a step, a state and no target (the maintenance
               passes), and Svelte refuses duplicate keys — which took the whole Status page down
               while a batch of source resets waited in the queue. -->
          {#each jobRows.rows as job, index (index)}
            <div class="job-row">
              <span class="job-step">{jobStepLabel(job.step)}</span>
              <span class="job-state job-state--{job.state}" title={job.why ?? ''}>
                {job.state === 'pending' ? t('status.jobOwed') : job.state}
              </span>
              {#if job.target}
                <button class="job-target" onclick={() => navigate(sourcePath(job.target!))}>
                  <Icon name="sources" size={12} />
                  <span class="truncate" title={job.title || job.target}>{job.title || job.target.slice(0, 12)}</span>
                </button>
              {:else}
                <span class="muted small">{t('status.wholeVault')}</span>
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
              <span class="muted small">{tn('status.moreQueued', jobRows.hidden)}</span>
            </div>
          {/if}
        </div>
      {/if}
    </section>

    {#if status.summary}
      <section class="section">
        <div class="section-head">
          <h2>{t('status.summaries')}</h2>
          {#if status.summary.at}
            <span class="muted small">{t('status.checkedAt', { time: relTime(status.summary.at) })}</span>
          {/if}
        </div>
        <div class="stat-grid stat-grid--three">
          <div class="stat"><span class="stat-num">{n(status.summary.missing)}</span><span class="stat-label">{t('status.missing')}</span></div>
          <div class="stat"><span class="stat-num">{n(status.summary.queued)}</span><span class="stat-label">{t('status.summaryQueued')}</span></div>
          <div class="stat"><span class="stat-num">{n(status.summary.running)}</span><span class="stat-label">{t('status.summaryRunning')}</span></div>
        </div>
        <p class="muted small section-note">
          {t('status.summaryNote')}
        </p>
      </section>
    {/if}

    <section class="section">
      <div class="section-head"><h2>{t('status.clusters')}</h2></div>
      <div class="action-list">
        {#each clusterActions as action (action.label)}
          <div class="action-row">
            <button class="btn btn--secondary" disabled={busy(action.job) || repairing} onclick={() => void action.run()}>
              <span class="btn-text">{action.label}</span>
            </button>
            <span class="action-hint">{action.hint}</span>
          </div>
        {/each}
      </div>
    </section>

    <section class="section">
      <div class="section-head">
        <h2>{t('status.vocabulary')}</h2>
        <span class="grow"></span>
        <button class="btn btn--primary btn--sm" disabled={busy('vocabulary') || repairing} onclick={() => void curateVocabulary()}>
          <Icon name="spark" size={13} /> <span class="btn-text">{t('status.curate')}</span>
        </button>
        <button class="btn btn--secondary btn--sm" disabled={busy('vocabulary') || repairing} onclick={() => void retopicClaims()}>
          <Icon name="distill" size={13} /> <span class="btn-text">{t('status.retopic')}</span>
        </button>
      </div>
      <p class="muted small section-note">
        <b>{t('status.curate')}</b> {t('status.vocabNote')}
      </p>
      {#if system.vocabulary && system.vocabulary.length > 0}
        <div class="vocab-list">
          {#each system.vocabulary as vocab (vocab.namespace)}
            <div class="vocab-row">
              <span class="vocab-name">{vocab.namespace}</span>
              <span class="vocab-numbers">
                {tn('status.vocabNames', vocab.names)} · {tn('status.vocabUses', vocab.uses)} · {t('status.vocabSingletons', { count: vocab.singletons })}
                ({n(Math.round(vocab.singleton_share * 100))}%) · {t('status.vocabTop', { pct: Math.round(vocab.coverage * 100) })}
                {#if vocab.canonical || vocab.absorbed}· {t('status.vocabCanonical', { canonical: vocab.canonical, absorbed: vocab.absorbed })}{/if}
                {#if vocab.new?.length}· <b>{t('status.vocabNew', { count: vocab.new.length })}</b>{/if}
              </span>
              <span class="vocab-flags">
                {#if vocab.collisions?.length}
                  <span class="chip chip--warn" title={t('status.collisionsTitle')}>
                    {tn('status.collisions', vocab.collision_count ?? vocab.collisions.length)}
                  </span>
                {/if}
                {#if vocab.near_duplicates?.length}
                  <span class="chip" title={t('status.familiesTitle')}>
                    {tn('status.families', vocab.near_duplicate_count ?? vocab.near_duplicates.length)}
                  </span>
                {/if}
                {#if vocab.broad_tokens}
                  <span class="chip" title={t('status.broadTokensTitle')}>
                    {tn('status.broadTokens', vocab.broad_tokens)}
                  </span>
                {/if}
                {#if vocab.oversized_families}
                  <span class="chip" title={t('status.broadFamiliesTitle')}>
                    {tn('status.broadFamilies', vocab.oversized_families)}
                  </span>
                {/if}
              </span>
            </div>
          {/each}
        </div>
      {:else}
        <p class="muted small">{t('status.noVocabulary')}</p>
      {/if}
    </section>

    <section class="section">
      <div class="section-head"><h2>{t('status.vault')}</h2></div>
      {#if system.duplicates && system.duplicates.groups > 0}
        <p class="muted small section-note">
          {tn('status.duplicates', system.duplicates.files, { groups: tn('status.groupCount', system.duplicates.groups) })}{#if system.duplicates.example}{t('status.duplicatesExample', { example: system.duplicates.example })}{/if}. {t('status.duplicatesNote')}
        </p>
      {/if}
      <div class="action-list">
        {#each vaultActions as action (action.label)}
          <div class="action-row">
            <button class="btn btn--secondary" disabled={busy(action.job) || repairing} onclick={() => void action.run()}>
              <span class="btn-text">{action.label}</span>
            </button>
            <span class="action-hint">{action.hint}</span>
          </div>
        {/each}
        {#if duplicates && duplicates.groups > 0}
          <div class="action-row">
            <button class="btn btn--secondary" disabled={busy('collapse-atoms') || repairing} onclick={() => void collapseDuplicateClaims()}>
              <span class="btn-text">{t('status.collapseDuplicates')}</span>
            </button>
            <span class="action-hint">{t('status.collapseDuplicatesHint')}</span>
          </div>
        {/if}
      </div>
    </section>

    <section class="section details-section">
      <div class="section-head"><h2>{t('status.vaultDetails')}</h2></div>
      <div class="details-grid">
        <div class="kv"><span class="k">{t('status.kvVault')}</span><span class="v mono">{status.vault.root}</span></div>
        <div class="kv"><span class="k">{t('status.kvIndex')}</span><span class="v mono">{status.index.path}</span></div>
        <div class="kv"><span class="k">{t('status.kvSources')}</span><span class="v mono">{status.vault.sources}</span></div>
        <div class="kv"><span class="k">{t('status.kvInbox')}</span><span class="v mono">{status.vault.inbox}</span></div>
        <div class="kv"><span class="k">{t('status.kvLanguageUI')}</span><span class="v mono">{status.language.ui}</span></div>
        <div class="kv"><span class="k">{t('status.kvLanguageClaims')}</span><span class="v mono">{status.language.claims}</span></div>
        <div class="kv"><span class="k">{t('status.kvLanguageSummary')}</span><span class="v mono">{status.language.summary}</span></div>
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

  .page-notice {
    margin-bottom: 16px;
  }

  /* The index rebuild outranks the counters: while it runs, nothing can be written. */
  .repair {
    margin-bottom: 18px;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
  }

  .repair-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .repair-count {
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }

  .repair-bar {
    height: 4px;
    margin: 9px 0 7px;
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
    overflow: hidden;
  }

  .repair-bar span {
    display: block;
    height: 100%;
    background: var(--accent);
    transition: width 0.4s ease;
  }

  .repair .section-note {
    margin: 0;
  }

  .stat-grid {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
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
    min-width: 0;
    min-height: 35px;
    overflow: hidden;
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
    overflow: hidden;
    color: var(--ink-2);
    font-weight: 550;
    text-overflow: ellipsis;
    white-space: nowrap;
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
    flex: 0 1 auto;
    gap: 5px;
    min-width: 0;
    max-width: min(260px, 100%);
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
    flex: 0 1 auto;
    min-width: 0;
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
    min-width: 0;
    flex: none;
    justify-content: flex-start;
  }

  .action-row .btn-text {
    flex: 1;
    text-align: left;
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

  .section-head {
    flex-wrap: wrap;
    row-gap: 6px;
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

  .details-grid .v {
    word-break: break-all;
  }

  @media (max-width: 860px) {
    .stat-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .stat-grid--three {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }

    .action-row {
      align-items: flex-start;
      flex-direction: column;
      gap: 5px;
    }

    .action-row .btn {
      width: auto;
      max-width: 100%;
    }

    .details-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  @media (max-width: 560px) {
    .stat-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .stat-grid--three {
      grid-template-columns: repeat(1, minmax(0, 1fr));
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
