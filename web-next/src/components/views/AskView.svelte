<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { deleteSavedAsk } from '../../lib/actions';
  import { relTime } from '../../lib/format';
  import { askPath, navigate, router } from '../../lib/router.svelte';
  import type { AskSummary } from '../../lib/api/types';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { asks } from '../../lib/stores/asks.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import CopyId from '../ui/CopyId.svelte';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import Markdown from '../ui/Markdown.svelte';
  import VirtualList from '../ui/VirtualList.svelte';

  const route = $derived(router.current);
  const detailOpen = $derived(route.view === 'ask' && !!route.id);
  const selected = $derived(asks.selected);
  const topics = $derived(claims.topics.map((topic) => topic.topic));

  let question = $state('');
  let tagFilter = $state('');
  let input: HTMLInputElement | undefined = $state();

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

  $effect(() => {
    if (!detailOpen && !asks.busy) input?.focus();
  });

  function submit(): void {
    const value = question.trim();
    if (!value || asks.busy) return;
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
</script>

{#snippet savedQueries()}
  {#if tagCloud.length > 0}
    <div class="tag-cloud" use:scrollFade use:rememberScroll={'ask-tags'} aria-label="Saved query tags">
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
      <span class="muted">Tag</span>
      <button class="chip chip--accent" onclick={() => (tagFilter = '')}>
        {tagFilter}<Icon name="close" size={10} />
      </button>
    </div>
  {/if}
  <div class="saved-asks" use:scrollFade use:rememberScroll={'ask-list'}>
    {#if asks.error}
      <p class="notice notice--error">{asks.error}</p>
    {:else if !asks.loaded}
      <div class="stack">
        {#each Array(4) as _, index (index)}<span class="skeleton" style="height:40px"></span>{/each}
      </div>
    {:else if visibleAsks.length === 0}
      <p class="empty-note">{tagFilter ? 'No saved query carries this tag.' : 'No saved queries yet.'}</p>
    {:else}
      <VirtualList items={visibleAsks} rowHeight={60} key={(ask) => ask.id}>
        {#snippet row(ask: AskSummary)}
          <button class="saved-row" class:active={selected?.id === ask.id} onclick={() => openSaved(ask.id)}>
            <Icon name="ask" size={15} />
            <span class="saved-main">
              <span class="saved-question">{ask.question}</span>
              <span class="saved-time">
                {relTime(ask.updated_at)}{#if ask.tags?.length} · {ask.tags.slice(0, 2).join(' / ')}{/if}
              </span>
            </span>
          </button>
        {/snippet}
      </VirtualList>
    {/if}
  </div>
{/snippet}

<div class="page ask-page" class:split={detailOpen}>
  {#if detailOpen}
    <section class="ask-list-pane" aria-label="Saved queries">
      <div class="list-head">
        <div class="list-title">
          <h1>Saved queries</h1>
          {#if asks.count}<span class="count">{asks.count}</span>{/if}
        </div>
        <p class="list-sub">Every answer is saved automatically.</p>
      </div>
      {@render savedQueries()}
    </section>
  {/if}

  <section class="ask-main" class:split={detailOpen}>
    {#if detailOpen}
      <a class="back-link" href="#/ask">← Saved queries</a>
    {:else}
      <div class="page-head ask-head">
        <div>
          <h1>Ask</h1>
          <p class="sub">Ask the claims; every answer is saved with its citations.</p>
        </div>
      </div>
    {/if}

    <form class="composer" onsubmit={(event) => { event.preventDefault(); submit(); }}>
      <div class="composer-label">{detailOpen ? 'Ask another question' : 'Your question'}</div>
      <div class="composer-row">
        <input
          use:clearable
          bind:this={input}
          class="input composer-input"
          bind:value={question}
          placeholder="What do the claims say about…"
          autocomplete="off"
          disabled={asks.busy}
          aria-label="Ask a question"
        />
        <button class="btn btn--primary" type="submit" disabled={asks.busy || !question.trim()}>
          {#if asks.busy}<span class="spinner"></span>{/if}
          Ask
        </button>
      </div>
    </form>

    <div class="ask-reading" class:detail-frame={detailOpen}>
      {#if asks.busy}
        <div class="thinking">
          <span class="spinner"></span>
          <div>
            <div class="thinking-title">Searching the claims…</div>
            {#if asks.question}<div class="thinking-question">{asks.question}</div>{/if}
            <div class="thinking-note">The answer is saved when the run completes.</div>
          </div>
        </div>
      {:else if detailOpen}
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
              <div class="kicker">Saved query</div>
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
                <CopyId text={selected.id} label="Ask ID" display={selected.id.slice(0, 10)} />
                <button class="btn btn--danger btn--sm" onclick={() => deleteSavedAsk(selected!.id)}>
                  <Icon name="trash" size={13} /> Delete
                </button>
              </div>
            </header>
            <Markdown text={selected.answer} {topics} footnotes />
          </article>
        {/if}
      {:else if asks.notice}
        <p class="notice">{asks.notice}</p>
      {:else if asks.error}
        <p class="notice notice--error">{asks.error}</p>
      {:else if asks.loaded && asks.items.length === 0}
        <EmptyState
          icon="ask"
          title="Ask a question"
          text="Thought Keeper searches the claims, follows their associations and cites what it used. Every answer is saved."
        />
      {/if}
    </div>

    {#if !detailOpen && asks.items.length > 0}
      <section class="saved-section">
        <div class="section-head">
          <h2>Saved queries</h2>
          <span class="count">{asks.count}</span>
        </div>
        {@render savedQueries()}
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
    border: 0;
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

  .saved-row :global(.ico) {
    color: var(--muted);
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

  .ask-reading.detail-frame .thinking {
    padding: 0;
    border: 0;
    background: transparent;
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

  .thinking {
    display: flex;
    align-items: flex-start;
    gap: 11px;
    padding: 16px;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    color: var(--muted);
  }

  .thinking .spinner {
    margin-top: 2px;
    color: var(--accent);
  }

  .thinking-title {
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 550;
  }

  .thinking-question {
    margin-top: 3px;
    color: var(--ink-2);
    font-family: var(--font-sans);
    font-size: 15px;
  }

  .thinking-note {
    margin-top: 5px;
    color: var(--faint);
    font-size: 11.5px;
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
  }
</style>
