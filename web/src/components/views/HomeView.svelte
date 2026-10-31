<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { api } from '../../lib/api';
  import { toggleReadLater } from '../../lib/actions';
  import type { Source } from '../../lib/api/types';
  import { relTime } from '../../lib/format';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { openAdd, openInterests } from '../../lib/overlays';
  import { readError, readSignal } from '../../lib/read';
  import { askPath, navigate, router, sourcePath } from '../../lib/router.svelte';
  import { pipelineLabel } from '../../lib/source-state';
  import { asks } from '../../lib/stores/asks.svelte';
  import { interests } from '../../lib/stores/interests.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { sourceWindow } from '../../lib/stores/source-window.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceLamp from '../sources/SourceLamp.svelte';

  let question = $state('');
  let newest = $state<Source[]>([]);
  let newestLoaded = $state(false);
  let newestError = $state('');
  // The read-later queue is the one place the interests show up before they are read: the three
  // oldest entries, with the rule that queued each one.
  let readLater = $state<Source[]>([]);
  let readLaterCount = $state(0);
  let readLaterLoaded = $state(false);

  const status = $derived(system.status);
  const recentQuestions = $derived(asks.recent.slice(0, 3));

  // What the application is doing RIGHT NOW. The front door is the one place a reader looks to
  // see whether a run is alive, so the pipeline speaks here instead of only in the top bar.
  const live = $derived(pipelineLabel(system.pipeline));

  const needs = $derived.by(() => {
    const index = status?.index;
    if (!index) return [];
    const items: { key: string; text: string; path: string }[] = [];
    if (index.failed > 0) {
      items.push({ key: 'failed', text: tn('home.needsFailed', index.failed), path: '#/attention' });
    }
    if (index.findings > 0) {
      items.push({ key: 'findings', text: tn('home.needsFindings', index.findings), path: '#/attention' });
    }
    if (index.unclustered > 0) {
      items.push({ key: 'unclustered', text: tn('home.needsUnclustered', index.unclustered), path: '#/status' });
    }
    return items;
  });

  // The vault in one line: the head says how much knowledge there is, not what the app is for.
  const pulse = $derived.by(() => {
    const index = status?.index;
    if (!index) return '';
    return [
      tn('home.pulse.sources', index.sources),
      tn('home.pulse.claims', index.atoms),
      tn('home.pulse.clusters', index.clusters),
    ].join(' · ');
  });

  // A vault with nothing in it is not a page with empty cards: it is one gesture.
  const emptyVault = $derived(!!status && status.index.sources === 0 && status.index.atoms === 0);

  $effect(() => {
    if (router.current.view !== 'home') return;
    // The Start page does not need the full list, only the newest ROOTS and the queue — but both
    // change when the vault does. `epoch` moves when the sources are invalidated, never when a page
    // of some list happens to arrive.
    void sourceWindow.epoch;
    untrack(() => {
      void loadNewest();
      void loadReadLater();
    });
  });

  $effect(() => {
    if (router.current.view !== 'home') return;
    // Everything else is owned by the event stream (sync.ts). What is read here is only what it
    // does not carry: the three questions the door shows and the rules that name a queue entry.
    untrack(() => {
      if (!system.status) void system.loadStatus();
      void asks.loadRecent();
      void interests.load();
    });
  });

  async function loadReadLater(): Promise<void> {
    try {
      const result = await api.sources({ readLaterOnly: true }, 3, 0);
      readLater = result.sources ?? [];
      readLaterCount = result.count ?? readLater.length;
      readLaterLoaded = true;
    } catch {
      readLaterLoaded = true;
    }
  }

  async function finishReadLater(hash: string): Promise<void> {
    await toggleReadLater(hash, false);
    await loadReadLater();
  }

  function viaLabel(source: Source): string {
    // Skip a slug whose rule was deleted: the queue entry stays, the dead label does not.
    const slugs = (source.read_later_via ?? []).filter((slug) => !!interests.bySlug(slug));
    if (slugs.length === 0) return '';
    const first = interests.nameOf(slugs[0]);
    return slugs.length > 1 ? `${first} +${slugs.length - 1}` : first;
  }

  async function loadNewest(): Promise<void> {
    try {
      // ROOTS only: a sitemap's pages and a feed's items are one addition, not fifty arrivals.
      const result = await api.sources({ rootsOnly: true }, 5, 0, readSignal(20_000));
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
    {#if pulse}<p class="sub">{pulse}</p>{/if}
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
        {#each recentQuestions as ask, index (ask.id)}
          {#if index > 0}<span class="sep">·</span>{/if}
          <button class="question-chip" onclick={() => navigate(askPath(ask.id))} title={ask.question}>
            {ask.question}
          </button>
        {/each}
      </div>
    {/if}
  </section>

  {#if live || needs.length > 0}
    <div class="home-signals">
      {#if live}
        <a class="signal signal--live" href="#/status">
          <span class="pulse-dot" aria-hidden="true"></span>
          <span class="signal-text">{t('home.working', { what: live })}</span>
          <Icon name="chevronRight" size={13} />
        </a>
      {/if}
      {#if needs.length > 0}
        <div class="signal signal--needs">
          <Icon name="warning" size={13} />
          <span class="signal-text">{t('home.needsYou')}</span>
          {#each needs as item, index (item.key)}
            {#if index > 0}<span class="sep">·</span>{/if}
            <a class="signal-chip" href={item.path}>{item.text}</a>
          {/each}
        </div>
      {/if}
    </div>
  {/if}

  {#if emptyVault}
    <section class="home-card onboarding">
      <p class="empty-title">{t('home.emptySourcesTitle')}</p>
      <p class="empty-text">{t('home.emptySourcesText')}</p>
      <p class="empty-text empty-text--quiet">{t('home.emptyInbox')}</p>
      <button class="btn btn--primary btn--sm" onclick={openAdd}>
        <Icon name="plus" size={13} /> {t('home.emptySourcesAction')}
      </button>
    </section>
  {:else}
    <div class="home-grid">
      <section class="home-card" aria-label={t('home.readLater')}>
        <div class="card-head">
          <h2>{t('home.readLater')}</h2>
          {#if readLaterLoaded && readLaterCount > 0}<span class="count">{n(readLaterCount)}</span>{/if}
          <span class="card-links">
            {#if readLaterCount > 0}
              <button
                class="icon-btn"
                title={t('interests.manage')}
                aria-label={t('interests.manage')}
                onclick={() => openInterests()}
              ><Icon name="target" size={13} /></button>
              <a class="card-link" href="#/sources?readlater=1">
                {t('home.allReadLater')} <Icon name="chevronRight" size={12} />
              </a>
            {/if}
          </span>
        </div>
        {#if !readLaterLoaded}
          <div class="stack">
            {#each Array(2) as _, index (index)}<span class="skeleton" style="height:36px"></span>{/each}
          </div>
        {:else if readLater.length === 0}
          <p class="quiet-line">
            {t('home.readLaterEmpty')}
            <button class="card-link card-link--button" onclick={() => openInterests()}>
              {t('home.readLaterEmptyAction')} <Icon name="chevronRight" size={12} />
            </button>
          </p>
        {:else}
          <div class="home-rows">
            {#each readLater as source (source.hash)}
              <div class="home-row saved-row">
                <button class="saved-open" onclick={() => openSource(source)}>
                  <SourceIcon kind={source.kind} hash={source.hash} size={16} />
                  <span class="source-main">
                    <span class="row-title">{source.title || source.hash.slice(0, 12)}</span>
                    <span class="row-sub">
                      {#if viaLabel(source)}<span class="saved-via"><Icon name="target" size={9} /> {viaLabel(source)}</span> · {/if}
                      {relTime(source.read_later_at ?? source.imported_at)}
                    </span>
                  </span>
                </button>
                <button
                  class="icon-btn saved-done"
                  title={t('home.readLaterDone')}
                  aria-label={t('home.readLaterDone')}
                  onclick={() => void finishReadLater(source.hash)}
                ><Icon name="check" size={13} /></button>
              </div>
            {/each}
          </div>
        {/if}
      </section>

      <section class="home-card" aria-label={t('home.newSources')}>
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
            {#each Array(3) as _, index (index)}<span class="skeleton" style="height:40px"></span>{/each}
          </div>
        {:else if newest.length === 0}
          <p class="quiet-line">{t('home.newSourcesEmpty')}</p>
        {:else}
          <div class="home-rows">
            {#each newest as source (source.hash)}
              {@const live = sources.progress[source.hash]}
              <button class="home-row source-row" onclick={() => openSource(source)}>
                <SourceIcon kind={source.kind} hash={source.hash} size={16} />
                <span class="source-main">
                  <span class="row-title">{source.title || source.hash.slice(0, 12)}</span>
                  <span class="row-sub">
                    {relTime(source.imported_at)}
                    {#if source.atom_count > 0} · {tn('home.claimsCount', source.atom_count)}{/if}
                  </span>
                </span>
                {#if !source.bookmark}<SourceLamp {source} {live} />{/if}
              </button>
            {/each}
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>

<style>
  .home-page {
    padding-top: 24px;
  }

  /* The same inner rhythm as the Ask lens's composer: one box, one set of paddings. */
  .home-ask {
    margin-bottom: 14px;
    padding: 13px 14px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .ask-label {
    margin-bottom: 7px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .ask-row {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .ask-input {
    height: 38px;
    font-size: 14px;
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
    gap: 5px;
    margin-top: 13px;
    padding-top: 11px;
    border-top: 1px solid var(--line);
  }

  .mini-label {
    margin-right: 3px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .question-chip {
    max-width: 100%;
    overflow: hidden;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .question-chip:hover {
    color: var(--accent);
  }

  /* Two thin signals, never cards: what runs and what waits. Both are absent when quiet. The 6px
     binds the two lines into ONE group; every gap between one surface and the next is 14px. */
  .home-signals {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0 0 14px;
  }

  .signal {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 7px;
    min-height: 30px;
    padding: 6px 11px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-muted);
    color: var(--ink-2);
    font-size: 12px;
    text-align: left;
  }

  a.signal:hover {
    border-color: var(--line-strong);
    color: var(--ink);
  }

  .signal :global(.ico) {
    flex: none;
    color: var(--faint);
  }

  .signal--needs :global(.ico) {
    color: var(--warning);
  }

  .signal-text {
    flex: none;
    color: var(--faint);
  }

  .signal--live .signal-text {
    color: var(--ink-2);
  }

  .signal-chip {
    overflow: hidden;
    color: var(--ink-2);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .signal-chip:hover {
    color: var(--accent);
    text-decoration: underline;
  }

  .sep {
    color: var(--faint);
  }

  .pulse-dot {
    flex: none;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--info);
    animation: breathe 1.4s var(--ease) infinite;
  }

  @keyframes breathe {
    50% {
      opacity: 0.35;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .pulse-dot {
      animation: none;
    }
  }

  .home-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.15fr);
    align-items: start;
    gap: 14px;
    margin-bottom: 14px;
  }

  .home-card {
    min-width: 0;
    padding: 15px 17px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .card-links {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
  }

  .card-links .card-link {
    margin-left: 0;
  }

  .card-links .icon-btn {
    width: 24px;
    height: 24px;
    color: var(--faint);
  }

  .card-links .icon-btn:hover {
    color: var(--accent);
  }

  .card-link--button {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0;
    border: 0;
    background: transparent;
    font: inherit;
    font-size: 11.5px;
  }

  .card-link--button:hover {
    color: var(--accent);
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

  .saved-row {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 0;
  }

  .saved-open {
    display: flex;
    align-items: center;
    gap: 9px;
    flex: 1;
    min-width: 0;
    padding: 7px 6px;
    border: 0;
    background: transparent;
    color: var(--ink-2);
    font: inherit;
    font-size: 12.5px;
    text-align: left;
  }

  .saved-open:hover {
    background: var(--bg-hover);
    color: var(--ink);
  }

  .saved-open :global(.ico) {
    color: var(--faint);
  }

  .saved-done {
    width: 26px;
    height: 26px;
    margin-right: 4px;
    opacity: 0;
  }

  .saved-row:hover .saved-done,
  .saved-row:focus-within .saved-done {
    opacity: 1;
  }

  .saved-via {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    color: var(--accent);
  }

  .saved-via :global(.ico) {
    color: var(--accent);
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

  /* The empty queue is one quiet line, never a box that asks for attention it does not have. */
  .quiet-line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin: 4px 0 2px;
    color: var(--faint);
    font-size: 12px;
    line-height: 1.5;
  }

  .quiet-line .card-link {
    margin-left: 0;
  }

  .onboarding {
    padding: 22px 24px;
  }

  .empty-title {
    margin: 0 0 6px;
    color: var(--ink);
    font-size: 15px;
    font-weight: 550;
  }

  .empty-text {
    margin: 0 0 10px;
    color: var(--muted);
    font-size: 12.5px;
    line-height: 1.55;
  }

  .empty-text--quiet {
    color: var(--faint);
    font-size: 11.5px;
  }

  @media (max-width: 860px) {
    .home-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  @media (max-width: 640px) {
    .ask-row {
      align-items: stretch;
      flex-direction: column;
    }

    .ask-row .btn {
      height: 38px;
    }
  }
</style>
