<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { hostOf, relTime } from '../../lib/format';
  import { openAdd } from '../../lib/overlays';
  import { navigate, router, sourcePath } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import type { Source } from '../../lib/api/types';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import VirtualList from '../ui/VirtualList.svelte';

  let query = $state('');

  const bookmarks = $derived(sources.items.filter((source) => source.bookmark));
  const grouped = $derived.by(() => {
    const term = query.trim().toLowerCase();
    const map = new Map<string, Source[]>();
    for (const source of bookmarks) {
      if (
        term &&
        !source.title.toLowerCase().includes(term) &&
        !(source.url ?? '').toLowerCase().includes(term) &&
        !(source.tags ?? []).some((tag) => tag.includes(term)) &&
        !(source.auto_tags ?? []).some((tag) => tag.includes(term))
      ) {
        continue;
      }
      const host = hostOf(source.url) || 'other';
      const list = map.get(host) ?? [];
      list.push(source);
      map.set(host, list);
    }
    return [...map.entries()]
      .map(([host, items]) => ({ host, items }))
      .sort((a, b) => a.host.localeCompare(b.host));
  });

  $effect(() => {
    if (router.current.view === 'bookmarks') void sources.loadList();
  });

  function open(source: Source): void {
    navigate(sourcePath(source.hash));
  }
</script>

<div class="page bookmarks-page">
  <div class="page-head">
    <div>
      <h1>Bookmarks</h1>
      <p class="sub">Collected links that exist as metadata only — not indexed, still yours.</p>
    </div>
    <span class="spacer"></span>
    <button class="btn btn--primary" onclick={openAdd}>
      <Icon name="plus" size={14} /> Add link
    </button>
  </div>

  {#if bookmarks.length > 0}
    <div class="toolbar">
      <div class="search-wrap bookmark-search">
        <Icon name="search" size={14} />
        <input use:clearable class="input input--search" placeholder="Filter bookmarks…" bind:value={query} aria-label="Filter bookmarks" />
      </div>
      <span class="muted small">{bookmarks.length} bookmark{bookmarks.length === 1 ? '' : 's'}</span>
    </div>
  {/if}

  {#if sources.error}
    <p class="notice notice--error">
      {sources.error}
      <button class="btn btn--ghost btn--sm" onclick={() => void sources.loadList()}>Try again</button>
    </p>
  {/if}
  {#if !sources.loaded}
    {#if connection.state !== 'live'}
      <EmptyState icon="bookmarks" title="Nothing loaded" text="Thought Keeper is not reachable, so this is not the vault's answer." />
    {:else}
      <div class="stack">
        {#each Array(4) as _, index (index)}<span class="skeleton" style="height:48px"></span>{/each}
      </div>
    {/if}
  {:else if bookmarks.length === 0}
    {#if !sources.error}
      <EmptyState
        icon="bookmarks"
        title="No bookmarks yet"
        text="Paste any link. Pages that cannot be read — video, paywall, app — are collected here instead of being silently dropped."
      >
        <button class="btn btn--primary btn--sm" onclick={openAdd}>
          <Icon name="plus" size={13} /> Add a link
        </button>
      </EmptyState>
    {/if}
  {:else if grouped.length === 0}
    <EmptyState icon="filter" title="No bookmark matches" text="Clear the filter to see every collected link." />
  {:else}
    {#each grouped as group (group.host)}
      <section class="host-group">
        <div class="host-head">
          <Icon name="link" size={13} />
          <h2>{group.host}</h2>
          <span class="count">{group.items.length}</span>
        </div>
        <div class="bookmark-list">
          <VirtualList items={group.items} rowHeight={52} key={(source) => source.hash}>
            {#snippet row(source: Source)}
              <div class="bookmark-row">
                <button class="bookmark-open" onclick={() => open(source)}>
                  <span class="bookmark-icon"><SourceIcon kind={source.kind} hash={source.hash} size={16} /></span>
                  <span class="bookmark-main">
                    <span class="bookmark-title">{source.title || source.url}</span>
                    <span class="bookmark-url">{source.url}</span>
                  </span>
                  <span class="bookmark-meta">
                    {#if source.url_status}{source.url_status} · {/if}
                    {source.url_checked_at ? `checked ${relTime(source.url_checked_at)}` : relTime(source.imported_at)}
                  </span>
                </button>
                <button
                  class="icon-btn"
                  title="Open the link in the browser"
                  onclick={() => {
                    if (source.url) window.open(source.url, '_blank', 'noopener');
                  }}
                >
                  <Icon name="external" size={14} />
                </button>
              </div>
            {/snippet}
          </VirtualList>
        </div>
      </section>
    {/each}
  {/if}
</div>

<style>
  .bookmarks-page {
    padding-top: 24px;
  }

  .bookmark-search {
    width: min(340px, 100%);
  }

  .host-group {
    margin-bottom: 22px;
  }

  .host-head {
    display: flex;
    align-items: center;
    gap: 7px;
    margin-bottom: 7px;
    color: var(--faint);
  }

  .host-head h2 {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
    font-weight: 650;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .host-head .count {
    font-size: 10.5px;
  }

  .bookmark-list {
    border-top: 1px solid var(--line);
  }

  .bookmark-row {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 52px;
    padding: 0 9px;
    border-bottom: 1px solid var(--line);
    transition: background 0.12s var(--ease);
  }

  .bookmark-row:hover {
    background: var(--bg-hover);
  }

  .bookmark-open {
    display: flex;
    align-items: center;
    gap: 11px;
    flex: 1;
    min-width: 0;
    padding: 9px 0;
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
  }

  .bookmark-open:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
    border-radius: var(--radius-sm);
  }

  .bookmark-icon {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    flex: none;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg-elevated);
    color: var(--muted);
  }

  .bookmark-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 2px;
  }

  .bookmark-title {
    overflow: hidden;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .bookmark-url {
    overflow: hidden;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .bookmark-meta {
    flex: none;
    color: var(--faint);
    font-size: 11.5px;
  }

  @media (max-width: 640px) {
    .bookmark-meta {
      display: none;
    }
  }
</style>
