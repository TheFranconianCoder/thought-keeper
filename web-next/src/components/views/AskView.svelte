<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { askAgain, deleteSavedAsk } from '../../lib/actions';
  import { relTime } from '../../lib/format';
  import { t } from '../../lib/i18n/index.svelte';
  import { askPath, navigate, router } from '../../lib/router.svelte';
  import type { AskSummary } from '../../lib/api/types';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { asks, type PendingAsk } from '../../lib/stores/asks.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import CopyId from '../ui/CopyId.svelte';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import Markdown from '../ui/Markdown.svelte';
  import VirtualList from '../ui/VirtualList.svelte';

  type AskRow =
    | { kind: 'run'; key: string; run: PendingAsk }
    | { kind: 'saved'; key: string; ask: AskSummary };

  const route = $derived(router.current);
  const detailOpen = $derived(route.view === 'ask' && !!route.id);
  const selected = $derived(asks.selected);
  const topics = $derived(claims.topics.map((topic) => topic.topic));

  let question = $state('');
  let tagFilter = $state('');

  const tagCloud = $derived.by(() => {
    const counts = new Map<string, number>();
    for (const ask of asks.items) {
      for (const tag of ask.tags ?? []) counts.set(tag, (counts.get(tag) ?? 0) + 1);
    }
    return [...counts.entries()]
      .map(([tag, count]) => ({ tag, count }))
      .sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
  });

  const visibleAsks = $derived(
    tagFilter ? asks.items.filter((ask) => (ask.tags ?? []).includes(tagFilter)) : asks.items,
  );

  // One list, two row kinds: a question in flight sits where its saved answer will appear. The
  // reader never has to look at a separate "thinking" box to know what the app is doing.
  const rows = $derived<AskRow[]>([
    ...asks.pending.map((run) => ({ kind: 'run' as const, key: `run-${run.key}`, run })),
    ...visibleAsks.map((ask) => ({ kind: 'saved' as const, key: ask.id, ask })),
  ]);

  $effect(() => {
    const current = router.current;
    if (current.view !== 'ask') return;
    // Store methods read their own reactive fields. Call them untracked, or this effect would
    // depend on the state it just changed and reload the open query in a loop.
    untrack(() => {
      if (current.id) void asks.open(current.id);
      else asks.clearSelection();
      if (!asks.loaded) void asks.load();
      if (claims.topics.length === 0 || claims.topicsStale) void claims.loadTopics();
    });
  });

  function submit(): void {
    const value = question.trim();
    if (!value) return;
    question = '';
    tagFilter = '';
    void asks.submit(value);
  }

  function openSaved(id: string): void {
    navigate(askPath(id));
  }

  function chooseTag(tag: string): void {
    tagFilter = tagFilter === tag ? '' : tag;
    if (detailOpen) navigate('#/ask');
  }

  function act(event: MouseEvent, action: () => void): void {
    event.stopPropagation();
    action();
    if (event.detail > 0) (event.currentTarget as HTMLElement | null)?.blur();
  }
</script>

{#snippet askRows()}
  {#if tagCloud.length > 0}
    <div class="tag-cloud" use:scrollFade use:rememberScroll={'ask-tags'} aria-label={t('ask.tagsAria')}>
      {#each tagCloud as item (item.tag)}
        <button
          class="chip"
          class:chip--accent={tagFilter === item.tag}
          onclick={() => chooseTag(item.tag)}
        >
          {item.tag}<span class="chip-count">{item.count}</span>
        </button>
      {/each}
    </div>
  {/if}
  {#if tagFilter}
    <div class="tag-filter">
      <span class="muted">{t('ask.tag')}</span>
      <button class="chip chip--accent" onclick={() => (tagFilter = '')}>
        {tagFilter}<Icon name="close" size={10} />
      </button>
    </div>
  {/if}
  <div class="saved-asks" use:scrollFade use:rememberScroll={'ask-list'}>
    {#if asks.error && rows.length === 0}
      <p class="notice notice--error">{asks.error}</p>
    {:else if !asks.loaded && rows.length === 0}
      <div class="stack">
        {#each Array(4) as _, index (index)}<span class="skeleton" style="height:40px"></span>{/each}
      </div>
    {:else if rows.length === 0}
      <p class="empty-note">{tagFilter ? t('ask.noTagMatch') : t('ask.noSaved')}</p>
    {:else}
      {#if asks.error}<p class="notice notice--error">{asks.error}</p>{/if}
      <VirtualList items={rows} rowHeight={62} key={(row) => row.key}>
        {#snippet row(row: AskRow)}
          {#if row.kind === 'run'}
            <div class="saved-row run-row" class:run-row--failed={row.run.status === 'failed'}>
              <span class="run-mark"><Icon name="ask" size={15} /></span>
              <span class="saved-main">
                <span class="saved-question">{row.run.question}</span>
                {#if row.run.status === 'failed'}
                  <span class="run-status run-status--failed" title={row.run.error ?? ''}>
                    <Icon name="error" size={11} />
                    <span class="truncate">{row.run.error}</span>
                  </span>
                {:else}
                  <span class="run-status">
                    <span class="spinner spinner--sm"></span>
                    <span>{row.run.status === 'starting' ? t('ask.starting') : t('ask.searching')}</span>
                  </span>
                {/if}
              </span>
              {#if row.run.status === 'failed'}
                <div class="saved-actions saved-actions--shown">
                  <button
                    class="icon-btn"
                    title={t('ask.askAgain')}
                    onclick={(event) => act(event, () => asks.retry(row.run.key))}
                  >
                    <Icon name="refresh" size={14} />
                  </button>
                  <button
                    class="icon-btn"
                    title={t('ask.dismissRun')}
                    onclick={(event) => act(event, () => asks.dismiss(row.run.key))}
                  >
                    <Icon name="close" size={14} />
                  </button>
                </div>
              {/if}
            </div>
          {:else}
            <div class="saved-row" class:active={selected?.id === row.ask.id}>
              <button class="saved-open" onclick={() => openSaved(row.ask.id)}>
                <Icon name="ask" size={15} />
                <span class="saved-main">
                  <span class="saved-question">{row.ask.question}</span>
                  <span class="saved-time">
                    {relTime(row.ask.updated_at)}{#if row.ask.tags?.length} · {row.ask.tags.slice(0, 2).join(' / ')}{/if}
                  </span>
                </span>
              </button>
              <div class="saved-actions">
                <button
                  class="icon-btn"
                  title={t('ask.askAgain')}
                  onclick={(event) => act(event, () => askAgain(row.ask.question))}
                >
                  <Icon name="refresh" size={14} />
                </button>
                <button
                  class="icon-btn icon-btn--danger"
                  title={t('common.delete')}
                  onclick={(event) => act(event, () => void deleteSavedAsk(row.ask.id))}
                >
                  <Icon name="trash" size={14} />
                </button>
              </div>
            </div>
          {/if}
        {/snippet}
      </VirtualList>
    {/if}
  </div>
{/snippet}

<div class="page ask-page" class:split={detailOpen}>
  {#if detailOpen}
    <section class="ask-list-pane" aria-label={t('ask.savedQueries')}>
      <div class="list-head">
        <div class="list-title">
          <h1>{t('ask.savedQueries')}</h1>
          {#if asks.count}<span class="count">{asks.count}</span>{/if}
        </div>
        <p class="list-sub">{t('ask.savedSub')}</p>
      </div>
      {@render askRows()}
    </section>
  {/if}

  <section class="ask-main" class:split={detailOpen}>
    {#if detailOpen}
      <a class="back-link" href="#/ask">{t('ask.back')}</a>
    {:else}
      <div class="page-head ask-head">
        <div>
          <h1>{t('ask.title')}</h1>
          <p class="sub">{t('ask.sub')}</p>
        </div>
      </div>
    {/if}

    <form class="composer" onsubmit={(event) => { event.preventDefault(); submit(); }}>
      <div class="composer-label">{detailOpen ? t('ask.another') : t('ask.yourQuestion')}</div>
      <div class="composer-row">
        <input
          use:clearable
          class="input composer-input"
          bind:value={question}
          placeholder={t('ask.placeholder')}
          autocomplete="off"
          aria-label={t('ask.ariaQuestion')}
        />
        <button class="btn btn--primary" type="submit" disabled={!question.trim()}>
          {t('ask.submit')}
        </button>
      </div>
    </form>

    {#if detailOpen}
      <div class="ask-reading detail-frame">
        {#if asks.detailError}
          <p class="notice notice--error">{asks.detailError}</p>
        {:else if asks.detailLoading && !selected}
          <div class="stack">
            <span class="skeleton" style="height:28px;width:60%"></span>
            <span class="skeleton" style="height:180px"></span>
          </div>
        {:else if selected}
          <article class="answer">
            <header class="answer-head">
              <div class="kicker">{t('ask.kicker')}</div>
              <h2>{selected.question}</h2>
              {#if selected.tags?.length}
                <div class="answer-tags">
                  {#each selected.tags as tag (tag)}
                    <button class="chip chip--accent" onclick={() => chooseTag(tag)}>{tag}</button>
                  {/each}
                </div>
              {/if}
              <div class="answer-meta">
                <span class="chip">{relTime(selected.updated_at)}</span>
                <span class="grow"></span>
                <CopyId text={selected.id} label={t('ask.idLabel')} display={selected.id.slice(0, 10)} />
                <button
                  class="btn btn--secondary btn--sm btn--action"
                  title={t('ask.askAgain')}
                  onclick={() => askAgain(selected!.question)}
                >
                  <Icon name="refresh" size={13} /> <span class="btn-label">{t('ask.againShort')}</span>
                </button>
                <button
                  class="btn btn--danger btn--sm btn--action"
                  title={t('common.delete')}
                  onclick={() => deleteSavedAsk(selected!.id)}
                >
                  <Icon name="trash" size={13} /> <span class="btn-label">{t('common.delete')}</span>
                </button>
              </div>
            </header>
            <Markdown text={selected.answer} {topics} footnotes />
          </article>
        {/if}
      </div>
    {:else if asks.loaded && rows.length === 0}
      <div class="ask-reading">
        {#if asks.error}
          <p class="notice notice--error">{asks.error}</p>
        {:else}
          <EmptyState icon="ask" title={t('ask.emptyTitle')} text={t('ask.emptyText')} />
        {/if}
      </div>
    {/if}

    {#if !detailOpen && rows.length > 0}
      <section class="saved-section">
        <div class="section-head">
          <h2>{t('ask.savedQueries')}</h2>
          {#if asks.count}<span class="count">{asks.count}</span>{/if}
        </div>
        {@render askRows()}
      </section>
    {/if}
  </section>
</div>

<style>
  .ask-page {
    padding-top: 24px;
  }

  .ask-page.split {
    display: grid;
    grid-template-columns: minmax(250px, 310px) minmax(0, 1fr);
    gap: 38px;
    align-items: start;
    height: auto;
    min-height: calc(100dvh - var(--topbar-h));
    max-width: 1280px;
    padding-top: 0;
    padding-bottom: 0;
  }

  .ask-list-pane {
    min-width: 0;
  }

  .split .ask-list-pane {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    height: calc(100dvh - var(--topbar-h));
    min-height: 0;
    max-height: calc(100dvh - var(--topbar-h));
    padding: 24px 0;
  }

  .list-title {
    display: flex;
    align-items: baseline;
    gap: 8px;
  }

  .list-title h1 {
    margin: 0;
    font-size: 24px;
    font-weight: 650;
    letter-spacing: -0.025em;
  }

  .list-title .count {
    color: var(--faint);
    font-size: 12px;
  }

  .list-sub {
    margin: 3px 0 12px;
    color: var(--faint);
    font-size: 11.5px;
  }

  .tag-cloud {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    max-height: 76px;
    margin: 0 0 8px;
    overflow: auto;
  }

  .tag-filter {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0 0 8px;
    font-size: 11.5px;
  }

  .saved-asks {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    border-top: 1px solid var(--line);
  }

  .saved-row {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    padding: 10px 9px;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: inherit;
    text-align: left;
    transition: background 0.12s var(--ease);
  }

  .saved-row:hover {
    background: var(--bg-hover);
  }

  .saved-row.active {
    background: var(--accent-soft);
  }

  .saved-open {
    display: flex;
    flex: 1;
    min-width: 0;
    align-items: center;
    gap: 9px;
    padding: 0;
    border: 0;
    background: transparent;
    color: inherit;
    text-align: left;
  }

  .saved-row :global(.ico) {
    color: var(--muted);
  }

  .run-mark {
    display: flex;
    color: var(--muted);
  }

  .run-row--failed .run-mark :global(.ico) {
    color: var(--danger);
  }

  .saved-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 2px;
  }

  .saved-question {
    overflow: hidden;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 500;
    line-height: 1.45;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .saved-time {
    overflow: hidden;
    color: var(--faint);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .run-status {
    display: flex;
    align-items: center;
    gap: 5px;
    overflow: hidden;
    color: var(--muted);
    font-size: 11.5px;
    white-space: nowrap;
  }

  .run-status--failed {
    color: var(--danger);
  }

  .run-status .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .saved-actions {
    display: flex;
    flex: none;
    align-items: center;
    gap: 1px;
    opacity: 0;
    transition: opacity 0.12s var(--ease);
  }

  .saved-row:hover .saved-actions,
  .saved-row:focus-within .saved-actions,
  .saved-actions--shown {
    opacity: 1;
  }

  .saved-actions .icon-btn {
    width: 26px;
    height: 26px;
  }

  .empty-note {
    margin: 0;
    padding: 14px 8px;
    color: var(--faint);
    font-size: 12px;
    line-height: 1.55;
  }

  .ask-main {
    min-width: 0;
  }

  .ask-main.split {
    padding-top: 24px;
  }

  .ask-reading.detail-frame {
    margin: 0 0 40px;
  }

  .ask-reading.detail-frame .answer {
    padding-bottom: 0;
  }

  .ask-head {
    margin-bottom: 16px;
  }

  .back-link {
    display: none;
    margin-bottom: 12px;
    color: var(--muted);
    font-size: 12px;
  }

  .composer {
    margin-bottom: 24px;
    padding: 13px 14px;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .composer-label {
    margin-bottom: 7px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .composer-row {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .composer-input {
    height: 38px;
    font-size: 14px;
  }

  .answer {
    padding-bottom: 30px;
  }

  .answer-head {
    margin-bottom: 20px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--line);
  }

  .kicker {
    margin-bottom: 5px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .answer-head h2 {
    margin: 0 0 8px;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 27px;
    font-weight: 600;
    line-height: 1.3;
    letter-spacing: -0.015em;
  }

  .answer-meta {
    display: flex;
    align-items: center;
    gap: 7px;
  }

  .answer-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin: 0 0 10px;
  }

  .saved-section {
    margin-top: 30px;
  }

  @media (max-width: 860px) {
    .ask-page.split {
      display: block;
      height: auto;
      min-height: 0;
      max-width: var(--content-max);
      padding: 22px 20px 56px;
    }

    .ask-page.split .ask-list-pane {
      display: none;
    }

    .back-link {
      display: inline-block;
    }

    .ask-list-pane {
      position: static;
      max-height: none;
    }

    .saved-asks {
      overflow: visible;
    }

    .ask-main.split {
      height: auto;
      padding-top: 0;
      overflow: visible;
      padding-right: 0;
    }

    .ask-reading.detail-frame {
      margin: 0;
    }

    .saved-actions {
      display: none;
    }

    .saved-actions--shown {
      display: flex;
    }
  }
</style>
