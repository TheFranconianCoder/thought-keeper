<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { hostOf, relTime } from '../../lib/format';
  import { t } from '../../lib/i18n/index.svelte';
  import { openAdd } from '../../lib/overlays';
  import { navigate, sourcePath } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { bookmarkWindow } from '../../lib/stores/source-window.svelte';
  import type { Source } from '../../lib/api/types';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceWindowList from '../sources/SourceWindowList.svelte';
  import ListHead from '../ui/ListHead.svelte';

  let query = $state('');
  const hasQuery = $derived(query.trim().length > 0);

  // The list is a WINDOW over the bookmark filter: the server counts the whole bookmark list, the
  // view reads the rows around the viewport. A host is a row label now, not a group heading that
  // would break at a page boundary.
  bookmarkWindow.setFilter({ bookmarksOnly: true });

  let filterTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const term = query.trim();
    clearTimeout(filterTimer);
    filterTimer = setTimeout(() => {
      bookmarkWindow.setFilter({ bookmarksOnly: true, query: term });
    }, 100);
    return () => clearTimeout(filterTimer);
  });

  $effect(() => {
    // A reset discards the pages. With a count, the list asks for the visible range itself; only an
    // empty/not-yet-known list needs the first page here.
    void bookmarkWindow.epoch;
    if (!bookmarkWindow.loaded || bookmarkWindow.count === 0) {
      void bookmarkWindow.ensure(0, 30);
    }
  });

  function retry(): void {
    bookmarkWindow.reset();
  }

  function open(source: Source): void {
    navigate(sourcePath(source.hash));
  }

  function host(source: Source): string {
    return hostOf(source.url) || t('bookmarks.otherHost');
  }
</script>

<div class="page bookmarks-page">
  <ListHead title={t('view.bookmarks')} count={bookmarkWindow.loaded ? bookmarkWindow.count : null}>
    {#snippet action()}
      <button class="btn btn--primary" onclick={openAdd}>
        <Icon name="plus" size={14} /> <span class="btn-text">{t('bookmarks.add')}</span>
      </button>
    {/snippet}

    <div class="search-wrap list-search">
      <Icon name="search" size={14} />
      <input
        use:clearable
        class="input input--search"
        placeholder={t('bookmarks.filterPlaceholder')}
        bind:value={query}
        aria-label={t('bookmarks.filterAria')}
      />
    </div>
  </ListHead>

  {#if bookmarkWindow.error}
    <p class="notice notice--error">
      {t('bookmarks.loadFailed', { error: bookmarkWindow.error })}
      <button class="btn btn--ghost btn--sm" onclick={retry}>{t('common.retry')}</button>
    </p>
  {/if}
  {#if !bookmarkWindow.loaded || (bookmarkWindow.loading && bookmarkWindow.count === 0)}
    {#if connection.state !== 'live'}
      <EmptyState icon="bookmarks" title={t('bookmarks.unreachableTitle')} text={t('bookmarks.unreachableText')} />
    {:else}
      <div class="stack">
        {#each Array(4) as _, index (index)}<span class="skeleton" style="height:48px"></span>{/each}
      </div>
    {/if}
  {:else if bookmarkWindow.count === 0}
    {#if !bookmarkWindow.error}
      {#if hasQuery}
        <EmptyState icon="filter" title={t('bookmarks.noMatch')} text={t('bookmarks.noMatchText')} />
      {:else}
        <EmptyState
          icon="bookmarks"
          title={t('bookmarks.emptyTitle')}
          text={t('bookmarks.emptyText')}
        >
          <button class="btn btn--primary btn--sm" onclick={openAdd}>
            <Icon name="plus" size={13} /> {t('bookmarks.addEmpty')}
          </button>
        </EmptyState>
      {/if}
    {/if}
  {:else}
    <div class="bookmark-list">
      <SourceWindowList
        count={bookmarkWindow.count}
        rowHeight={52}
        itemAt={(index) => bookmarkWindow.itemAt(index)}
        ensure={(start, end) => void bookmarkWindow.ensure(start, end)}
        epoch={bookmarkWindow.epoch}
      >
        {#snippet row(source: Source | undefined)}
          {#if source}
            <div class="bookmark-row">
              <button class="bookmark-open" onclick={() => open(source)}>
                <span class="bookmark-icon"><SourceIcon kind={source.kind} hash={source.hash} size={16} /></span>
                <span class="bookmark-main">
                  <span class="bookmark-title">{source.title || source.url}</span>
                  <span class="bookmark-url"><span class="bookmark-host">{host(source)}</span> · {source.url}</span>
                </span>
                <span class="bookmark-meta">
                  {#if source.url_status}{source.url_status} · {/if}
                  {source.url_checked_at ? t('bookmarks.checked', { time: relTime(source.url_checked_at) }) : relTime(source.imported_at)}
                </span>
              </button>
              <button
                class="icon-btn bookmark-external"
                title={t('bookmarks.openExternal')}
                onclick={() => {
                  if (source.url) window.open(source.url, '_blank', 'noopener');
                }}
              >
                <Icon name="external" size={14} />
              </button>
            </div>
          {:else}
            <div class="bookmark-row bookmark-row--skeleton" aria-hidden="true">
              <span class="skeleton bookmark-skeleton"></span>
            </div>
          {/if}
        {/snippet}
      </SourceWindowList>
    </div>
  {/if}
</div>

<style>
  .bookmarks-page {
    padding-top: 24px;
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

  .bookmark-row--skeleton {
    padding: 10px 9px;
  }

  .bookmark-row--skeleton:hover {
    background: transparent;
  }

  .bookmark-skeleton {
    width: 100%;
    height: 30px;
  }

  .bookmark-host {
    color: var(--muted);
    font-weight: 550;
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
    max-width: 45%;
    overflow: hidden;
    color: var(--faint);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 860px) {
    .bookmark-external {
      display: none;
    }
  }

  @media (max-width: 640px) {
    .bookmark-meta {
      display: none;
    }
  }
</style>
