<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { api } from '../../lib/api';
  import type { Source } from '../../lib/api/types';
  import { relTime } from '../../lib/format';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { openAdd } from '../../lib/overlays';
  import { readError, readSignal } from '../../lib/read';
  import { recent, type RecentEntry } from '../../lib/recent.svelte';
  import { askPath, claimPath, navigate, router, sourcePath, wikiPath } from '../../lib/router.svelte';
  import { statusLabel } from '../../lib/source-state';
  import { asks } from '../../lib/stores/asks.svelte';
  import { sourceWindow } from '../../lib/stores/source-window.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';

  let question = $state('');
  let newest = $state<Source[]>([]);
  let newestLoaded = $state(false);
  let newestError = $state('');

  const status = $derived(system.status);
  const recentItems = $derived(recent.items.slice(0, 3));
  const recentQuestions = $derived(asks.items.slice(0, 3));
  const newSources = $derived(newest);

  const needs = $derived.by(() => {
    const index = status?.index;
    if (!index) return [];
    const items: { key: string; text: string; icon: string; path: string }[] = [];
    if (index.failed > 0) {
      items.push({
        key: 'failed',
        text: tn('home.needsFailed', index.failed),
        icon: 'warning',
        path: '#/attention',
      });
    }
    if (index.findings > 0) {
      items.push({
        key: 'findings',
        text: tn('home.needsFindings', index.findings),
        icon: 'info',
        path: '#/attention',
      });
    }
    if (index.unclustered > 0) {
      items.push({
        key: 'unclustered',
        text: tn('home.needsUnclustered', index.unclustered),
        icon: 'layers',
        path: '#/status',
      });
    }
    return items;
  });

  const pulse = $derived.by(() => {
    const index = status?.index;
    if (!index) return '';
    return [
      tn('home.pulse.sources', index.sources),
      tn('home.pulse.claims', index.atoms),
      tn('home.pulse.clusters', index.clusters),
      tn('home.pulse.unclustered', index.unclustered),
    ].join(' · ');
  });

  $effect(() => {
    if (router.current.view !== 'home') return;
    // The Sources window's epoch moves when pages are discarded (a source write, a filter change).
    // The Start page does not need the full list, only the five newest rows.
    void sourceWindow.epoch;
    untrack(() => {
      void system.loadStatus();
      void system.loadPipeline();
      void asks.load();
      void loadNewest();
    });
  });

  async function loadNewest(): Promise<void> {
    try {
      const result = await api.sources({}, 5, 0, readSignal(20_000));
      newest = result.sources ?? [];
      newestLoaded = true;
      newestError = '';
    } catch (error) {
      newestLoaded = true;
      newestError = readError(error);
    }
  }

  function submit(): void {
    const value = question.trim();
    if (!value) return;
    question = '';
    // The question becomes a row in the Ask list with its status; that list is where it belongs.
    void asks.submit(value);
    navigate('#/ask');
  }

  function recentPath(entry: RecentEntry): string {
    switch (entry.kind) {
      case 'source':
        return sourcePath(entry.id);
      case 'claim':
        return claimPath(entry.id);
      case 'wiki':
        return wikiPath(entry.id);
      case 'ask':
        return askPath(entry.id);
      default:
        return '#/home';
    }
  }

  function kindLabel(entry: RecentEntry): string {
    return t(`home.kind.${entry.kind}`);
  }

  function openSource(source: Source): void {
    navigate(sourcePath(source.hash));
  }
</script>

<div class="page home-page">
  <div class="page-head">
    <div class="head-row">
      <h1>{t('home.title')}</h1>
      <span class="spacer"></span>
      <button class="btn btn--primary" onclick={openAdd}>
        <Icon name="plus" size={14} /> <span class="btn-text">{t('nav.addSource')}</span>
      </button>
    </div>
    <p class="sub">{t('home.sub')}</p>
  </div>

  <section class="home-ask" aria-label={t('home.ask')}>
    <div class="ask-label">{t('home.ask')}</div>
    <form
      class="ask-row"
      onsubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <input
        use:clearable
        class="input ask-input"
        bind:value={question}
        placeholder={t('home.askPlaceholder')}
        autocomplete="off"
        aria-label={t('home.askPlaceholder')}
      />
      <button class="btn btn--primary" type="submit" disabled={!question.trim()}>
        {t('home.askButton')}
      </button>
    </form>
    <p class="ask-note">{t('home.askNote')}</p>
    {#if recentQuestions.length > 0}
      <div class="recent-questions">
        <span class="mini-label">{t('home.recentQuestions')}</span>
        {#each recentQuestions as ask (ask.id)}
          <button class="question-chip" onclick={() => navigate(askPath(ask.id))} title={ask.question}>
            {ask.question}
          </button>
        {/each}
      </div>
    {/if}
  </section>

  <div class="home-grid">
    <section class="home-card">
      <div class="card-head">
        <h2>{t('home.continue')}</h2>
      </div>
      {#if recentItems.length === 0}
        <p class="quiet">{t('home.continueEmpty')}</p>
      {:else}
        <div class="home-rows">
          {#each recentItems as entry (entry.kind + entry.id)}
            <a class="home-row" href={recentPath(entry)}>
              <span class="row-kind">{kindLabel(entry)}</span>
              <span class="row-title">{entry.title}</span>
              <Icon name="chevronRight" size={14} />
            </a>
          {/each}
        </div>
      {/if}
    </section>

    <section class="home-card">
      <div class="card-head">
        <h2>{t('home.needs')}</h2>
        {#if needs.length > 0}<span class="count">{n(needs.length)}</span>{/if}
      </div>
      {#if !status}
        <p class="quiet">{t('common.loading')}</p>
      {:else if needs.length === 0}
        <p class="quiet">{t('home.allQuiet')}</p>
      {:else}
        <div class="home-rows">
          {#each needs as item (item.key)}
            <a class="home-row" href={item.path}>
              <Icon name={item.icon} size={15} />
              <span class="row-title">{item.text}</span>
              <Icon name="chevronRight" size={14} />
            </a>
          {/each}
        </div>
      {/if}
    </section>
  </div>

  <section class="home-card sources-card">
    <div class="card-head">
      <h2>{t('home.newSources')}</h2>
      <a class="card-link" href="#/sources">
        {t('home.allSources')}
        <Icon name="chevronRight" size={13} />
      </a>
    </div>
    {#if newestError}
      <p class="notice notice--error">{newestError}</p>
    {/if}
    {#if !newestLoaded}
      <div class="stack">
        {#each Array(3) as _, index (index)}<span class="skeleton" style="height:46px"></span>{/each}
      </div>
    {:else if newSources.length === 0}
      <div class="empty-sources">
        <p class="empty-title">{t('home.emptySourcesTitle')}</p>
        <p class="empty-text">{t('home.emptySourcesText')}</p>
        <button class="btn btn--primary btn--sm" onclick={openAdd}>
          <Icon name="plus" size={13} /> {t('home.emptySourcesAction')}
        </button>
      </div>
    {:else}
      <div class="home-rows">
        {#each newSources as source (source.hash)}
          <button class="home-row source-row" onclick={() => openSource(source)}>
            <SourceIcon kind={source.kind} hash={source.hash} size={16} />
            <span class="source-main">
              <span class="row-title">{source.title || source.hash.slice(0, 12)}</span>
              <span class="row-sub">
                {source.kind} · {relTime(source.imported_at)} · {statusLabel(source)}
              </span>
            </span>
            <Icon name="chevronRight" size={14} />
          </button>
        {/each}
      </div>
    {/if}
  </section>

  {#if pulse}
    <p class="home-pulse">{pulse}</p>
  {/if}
</div>

<style>
  .home-page {
    padding-top: 24px;
  }

  .home-ask {
    margin-bottom: 26px;
    padding: 18px 20px;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .ask-label {
    margin-bottom: 8px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .ask-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .ask-input {
    height: 42px;
    font-size: 15px;
  }

  .ask-note {
    margin: 7px 0 0;
    color: var(--faint);
    font-size: 11.5px;
  }

  .recent-questions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin-top: 13px;
    padding-top: 11px;
    border-top: 1px solid var(--line);
  }

  .mini-label {
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .question-chip {
    max-width: 100%;
    overflow: hidden;
    padding: 4px 9px;
    border: 1px solid var(--line);
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
    color: var(--ink-2);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .question-chip:hover {
    border-color: var(--line-strong);
    color: var(--ink);
  }

  .home-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: 18px;
    margin-bottom: 18px;
  }

  .home-card {
    min-width: 0;
    padding: 15px 17px;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .sources-card {
    margin-bottom: 18px;
  }

  .card-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }

  .card-head h2 {
    margin: 0;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  .card-head .count {
    color: var(--faint);
    font-size: 11.5px;
  }

  .card-link {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1;
    transition: color 0.14s var(--ease);
  }

  .card-link :global(.ico) {
    color: var(--faint);
    transition: color 0.14s var(--ease);
  }

  .card-link:hover,
  .card-link:hover :global(.ico) {
    color: var(--accent);
  }

  .home-rows {
    display: flex;
    flex-direction: column;
  }

  .home-row {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    min-height: 36px;
    padding: 7px 6px;
    border: 0;
    border-top: 1px solid var(--line);
    border-radius: 0;
    background: transparent;
    color: var(--ink-2);
    font-size: 12.5px;
    text-align: left;
  }

  .home-row:first-child {
    border-top: 0;
  }

  .home-row:hover {
    background: var(--bg-hover);
    color: var(--ink);
  }

  .home-row :global(.ico) {
    color: var(--faint);
  }

  .row-kind {
    flex: none;
    color: var(--faint);
    font-size: 10.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .row-title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .source-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .source-row .row-title {
    flex: none;
  }

  .row-sub {
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .quiet {
    margin: 4px 0 2px;
    color: var(--faint);
    font-size: 12px;
    line-height: 1.5;
  }

  .empty-sources {
    padding: 12px 4px 6px;
  }

  .empty-title {
    margin: 0 0 4px;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 550;
  }

  .empty-text {
    margin: 0 0 12px;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.5;
  }

  .home-pulse {
    margin: 0;
    color: var(--faint);
    font-size: 11px;
    text-align: center;
  }

  @media (max-width: 640px) {
    .home-ask {
      padding: 14px 15px;
    }

    .ask-row {
      align-items: stretch;
      flex-direction: column;
    }

    .ask-row .btn {
      height: 38px;
    }
  }
</style>
