<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { api } from '../../lib/api';
  import type { QueueCounts, Source } from '../../lib/api/types';
  import { pubDate, relTime } from '../../lib/format';
  import { t, tn } from '../../lib/i18n/index.svelte';
  import { openAdd } from '../../lib/overlays';
  import { readError, readSignal } from '../../lib/read';
  import { askPath, navigate, router, sourcePath } from '../../lib/router.svelte';
  import { pipelineLabel } from '../../lib/source-state';
  import { asks } from '../../lib/stores/asks.svelte';
  import { interests } from '../../lib/stores/interests.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { sourceWindow } from '../../lib/stores/source-window.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import Icon from '../ui/Icon.svelte';
  import InterestChip from '../ui/InterestChip.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceLamp from '../sources/SourceLamp.svelte';

  let question = $state('');
  let arrivals = $state<Source[]>([]);
  let arrivalCount = $state(0);
  let arrivalsLoaded = $state(false);
  let arrivalsError = $state('');
  // The queue is a LINE, not a card: the oldest entry and its numbers say everything the door owes
  // it, and the arrivals below are the list the reader actually works from.
  let readLaterOldest = $state<Source | null>(null);
  let readLaterCount = $state(0);
  let readLaterQueue = $state<QueueCounts | null>(null);
  let readLaterLoaded = $state(false);

  const status = $derived(system.status);
  const recentQuestions = $derived(asks.recent.slice(0, 3));
  // A card that would only say "nothing came in this window" is one card too many: like the signal
  // lines it is absent when quiet.
  const showArrivals = $derived(!arrivalsLoaded || arrivalsError !== '' || arrivals.length > 0);

  // The rules BEHIND the queue, by name and with their share: that is what makes the line say WHY
  // it is long. A slug whose rule was deleted is dropped, exactly as a queue row drops its label.
  const readLaterRules = $derived(
    (readLaterQueue?.interests ?? [])
      .filter((share) => !!interests.bySlug(share.slug))
      .map((share) => ({ slug: share.slug, name: interests.nameOf(share.slug), count: share.count })),
  );

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
    // The Start page does not need the full list, only the arrivals and the queue — but both change
    // when the vault does. `epoch` moves when the sources are invalidated, never when a page of some
    // list happens to arrive.
    void sourceWindow.epoch;
    untrack(() => {
      void loadArrivals();
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
      // One row is enough: the line names the oldest entry and the numbers come with the answer.
      const result = await api.sources({ readLaterOnly: true }, 1, 0);
      readLaterOldest = result.sources?.[0] ?? null;
      readLaterCount = result.queue?.open ?? result.count ?? 0;
      readLaterQueue = result.queue ?? null;
      readLaterLoaded = true;
    } catch {
      readLaterLoaded = true;
    }
  }

  async function loadArrivals(): Promise<void> {
    try {
      // The newest SOURCES of the window, each one a row of its own: a feed's article is an arrival
      // like a document is, and the window keeps the card from freezing on the newest ever.
      const since = new Date(Date.now() - 3 * 86_400_000).toISOString();
      const result = await api.sources({ since }, 8, 0, readSignal(20_000));
      arrivals = result.sources ?? [];
      arrivalCount = result.count ?? arrivals.length;
      arrivalsLoaded = true;
      arrivalsError = '';
    } catch (error) {
      arrivalsLoaded = true;
      arrivalsError = readError(error);
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

  {#if live || needs.length > 0 || readLaterCount > 0}
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
      {#if readLaterLoaded && readLaterCount > 0}
        <a class="signal signal--queue" href="#/sources?readlater=1">
          <Icon name="clock" size={13} />
          <span class="signal-text">{t('home.readLater')}</span>
          <span class="queue-fact">{tn('home.readLaterOpen', readLaterCount)}</span>
          {#each readLaterRules as rule (rule.slug)}
            <span class="sep">·</span>
            <span class="queue-fact">{rule.name} {rule.count}</span>
          {/each}
          {#if readLaterOldest}
            <span class="sep">·</span>
            <span class="queue-fact">
              {t('home.readLaterOldest', { age: relTime(readLaterOldest.read_later_at ?? readLaterOldest.imported_at) })}
            </span>
          {/if}
          <span class="queue-open">
            {t('home.allReadLater')} <Icon name="chevronRight" size={12} />
          </span>
        </a>
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
  {:else if showArrivals}
    <section class="home-card" aria-label={t('home.arrivals')}>
      <div class="card-head">
        <h2>{t('home.arrivals')}</h2>
        {#if arrivalsLoaded && arrivalCount > 0}
          <span class="count">{tn('home.arrivalsCount', arrivalCount)}</span>
        {/if}
        <span class="card-window">{t('home.arrivalsWindow')}</span>
        <a class="card-link" href="#/sources">
          {t('home.allSources')}
          <Icon name="chevronRight" size={13} />
        </a>
      </div>
      {#if arrivalsError}
        <p class="notice notice--error">{arrivalsError}</p>
      {/if}
      {#if !arrivalsLoaded}
        <div class="stack">
          {#each Array(4) as _, index (index)}<span class="skeleton" style="height:40px"></span>{/each}
        </div>
      {:else}
        <div class="home-rows">
          {#each arrivals as source (source.hash)}
            {@const live = sources.progress[source.hash]}
            <button class="home-row source-row" onclick={() => openSource(source)}>
              <SourceIcon kind={source.kind} hash={source.hash} size={16} />
              <span class="source-main">
                <span class="row-title">{source.title || source.hash.slice(0, 12)}</span>
                <span class="row-sub">
                  <span class="row-sub-text">
                    {#if source.published_at}{pubDate(source.published_at)} · {/if}
                    {t('home.arrivalAge', { age: relTime(source.imported_at) })}
                    {#if source.atom_count > 0} · {tn('home.claimsCount', source.atom_count)}{/if}
                  </span>
                  <InterestChip {source} />
                </span>
              </span>
              {#if !source.bookmark}<SourceLamp {source} {live} />{/if}
            </button>
          {/each}
        </div>
      {/if}
    </section>
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

  /* The queue is a STATE of the reader, not a second card: one line with the numbers behind it,
  and the way into the full list at the end. */
  .signal--queue {
    color: var(--ink-2);
  }

  .queue-fact {
    flex: none;
    color: var(--ink-2);
  }

  .queue-open {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    margin-left: auto;
    color: var(--muted);
  }

  a.signal:hover .queue-open {
    color: var(--accent);
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

  .home-card {
    min-width: 0;
    padding: 15px 17px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .card-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
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

  /* The window the card reads, said instead of assumed: the digest is a week, not "the newest". */
  .card-window {
    color: var(--faint);
    font-size: 11px;
    letter-spacing: 0.01em;
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

  /* The sub line is a ROW: the text gives way first, the rule chip keeps its shape. */
  .row-sub {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    color: var(--faint);
    font-size: 11px;
  }

  .row-sub-text {
    overflow: hidden;
    min-width: 0;
    text-overflow: ellipsis;
    white-space: nowrap;
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
