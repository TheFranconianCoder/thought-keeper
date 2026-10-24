<script lang="ts">
  import { onMount } from 'svelte';
  import { clearable } from '../../lib/clearable';
  import { captureSource, deleteSource, distillSource, openSourceFromHash, rebuildSourceTree, refreshSource, resetSource } from '../../lib/actions';
  import type { Source } from '../../lib/api/types';
  import { fmtBytes, relTime } from '../../lib/format';
  import { openAdd } from '../../lib/overlays';
  import { router } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { sources, type SourceKindFilter } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { rememberSourcesTop } from '../../lib/sources-scroll';
  import { sourcePath } from '../../lib/router.svelte';
  import { navigate } from '../../lib/router.svelte';
  import { statusLabel } from '../../lib/source-state';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceSteps from '../sources/SourceSteps.svelte';
  import VirtualList from '../ui/VirtualList.svelte';

  // The vault total comes from the status report, so the header is right from the first paint; the
  // list itself appears complete, never page by page.
  const totalSources = $derived(system.status?.index.sources ?? sources.count);

  // The tag cloud counts what the sources carry: human labels and the agent's auto tags, once per
  // source (the same rule the source-tags vocabulary uses).
  const tagCloud = $derived.by(() => {
    const counts = new Map<string, number>();
    for (const source of sources.items) {
      for (const tag of new Set([...(source.tags ?? []), ...(source.auto_tags ?? [])])) {
        counts.set(tag, (counts.get(tag) ?? 0) + 1);
      }
    }
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .map(([tag, count]) => ({ tag, count }));
  });

  const filtered = $derived.by(() => {
    const term = sources.query.trim().toLowerCase();
    return sources.items.filter((source) => {
      if (sources.kind !== 'all' && source.kind !== sources.kind) return false;
      if (sources.filter) {
        const tags = new Set([...(source.tags ?? []), ...(source.auto_tags ?? [])]);
        if (!tags.has(sources.filter)) return false;
      }
      if (!term) return true;
      return (
        source.title.toLowerCase().includes(term) ||
        source.original_name.toLowerCase().includes(term) ||
        (source.url ?? '').toLowerCase().includes(term) ||
        (source.tags ?? []).some((tag) => tag.includes(term)) ||
        (source.auto_tags ?? []).some((tag) => tag.includes(term))
      );
    });
  });

  $effect(() => {
    if (router.current.view === 'sources') void sources.loadList();
  });

  function setKind(next: SourceKindFilter): void {
    sources.kind = next;
  }

  // The list lives in the shell's page scroller. Keep its last offset so the detail's back button can
  // seed the fresh #/sources entry with it; the row click captures the exact value as well.
  onMount(() => {
    const scroller = document.querySelector<HTMLElement>('.content-scroll');
    if (!scroller) return;
    const save = (): void => rememberSourcesTop(scroller.scrollTop);
    scroller.addEventListener('scroll', save, { passive: true });
    // No save() here: on a return the scroller still holds the OUTGOING detail's offset for a tick.
    // The row click and every scroll while the list is visible capture the real position.
    return () => scroller.removeEventListener('scroll', save);
  });

  function open(source: Source): void {
    const scroller = document.querySelector<HTMLElement>('.content-scroll');
    if (scroller) rememberSourcesTop(scroller.scrollTop);
    navigate(sourcePath(source.hash));
  }

  function act(event: MouseEvent, action: () => void): void {
    event.stopPropagation();
    action();
    if (event.detail > 0) (event.currentTarget as HTMLElement | null)?.blur();
  }

  function subLine(source: Source): string {
    return source.original_name || source.url || source.hash.slice(0, 12);
  }

  /** rowTags shows up to four labels: the human's first, then the agent's (a label both carry is
   *  shown once). The rest is a "+N" chip; the tag cloud is the place to see them all. */
  function rowTags(source: Source): { shown: { tag: string; auto: boolean }[]; more: number } {
    const human = source.tags ?? [];
    const auto = (source.auto_tags ?? []).filter((tag) => !human.includes(tag));
    const all = [
      ...human.map((tag) => ({ tag, auto: false })),
      ...auto.map((tag) => ({ tag, auto: true })),
    ];
    return { shown: all.slice(0, 4), more: Math.max(0, all.length - 4) };
  }
</script>

<div class="page sources-page">
  <div class="page-head">
    <div>
      <h1>Sources</h1>
      <p class="sub">
        {#if sources.loaded || totalSources > 0}
          {totalSources} immutable source{totalSources === 1 ? '' : 's'} · each file or link is read once
        {:else}
          Loading sources…
        {/if}
      </p>
    </div>
    <span class="spacer"></span>
    <button class="btn btn--primary" onclick={openAdd}>
      <Icon name="plus" size={14} /> Add source
    </button>
  </div>

  <div class="toolbar">
    <div class="search-wrap source-search">
      <Icon name="search" size={14} />
      <input
        use:clearable
        class="input input--search"
        placeholder="Filter sources…"
        bind:value={sources.query}
        aria-label="Filter sources"
      />
    </div>
    <div class="seg" role="tablist" aria-label="Source kind">
      <button class:on={sources.kind === 'all'} onclick={() => setKind('all')}>All</button>
      <button class:on={sources.kind === 'md'} onclick={() => setKind('md')}>Markdown</button>
      <button class:on={sources.kind === 'pdf'} onclick={() => setKind('pdf')}>PDF</button>
      <button class:on={sources.kind === 'txt'} onclick={() => setKind('txt')}>Text</button>
      <button class:on={sources.kind === 'url'} onclick={() => setKind('url')}>URLs</button>
    </div>
  </div>

  {#if tagCloud.length > 0}
    <div class="tag-cloud" use:scrollFade use:rememberScroll={'source-tags'} aria-label="Source tags">
      {#each tagCloud as tag (tag.tag)}
        <button
          class="chip"
          class:chip--accent={sources.filter === tag.tag}
          onclick={() => (sources.filter = sources.filter === tag.tag ? '' : tag.tag)}
        >
          {tag.tag}<span class="chip-count">{tag.count}</span>
        </button>
      {/each}
    </div>
  {/if}
  {#if sources.filter}
    <div class="tag-filter">
      Filtered by <b>{sources.filter}</b>
      <button class="link-button" onclick={() => (sources.filter = '')}>clear</button>
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
      <EmptyState icon="sources" title="Nothing loaded" text="Thought Keeper is not reachable, so this is not the vault's answer." />
    {:else}
      <div class="stack">
        {#each Array(5) as _, index (index)}<span class="skeleton" style="height:52px"></span>{/each}
      </div>
    {/if}
  {:else if sources.items.length === 0}
    {#if !sources.error}
      <EmptyState
        icon="sources"
        title="No sources yet"
        text="Add a Markdown file, a PDF, plain text or a URL. Every source stays as a local, immutable snapshot."
      >
        <button class="btn btn--primary btn--sm" onclick={openAdd}>
          <Icon name="plus" size={13} /> Add the first source
        </button>
      </EmptyState>
    {/if}
  {:else if filtered.length === 0}
    <EmptyState icon="filter" title="No source matches" text="Clear the search, the tag or the kind filter." />
  {:else}
    <div class="source-list">
      <VirtualList items={filtered} rowHeight={78} key={(source) => source.hash}>
        {#snippet row(source: Source)}
          {@const live = sources.progress[source.hash]}
          {@const rowTagInfo = rowTags(source)}
          <div class="source-row">
            <button
              class="source-open"
              title={`${statusLabel(source, live)}`}
              onclick={() => open(source)}
            >
              <span class="source-icon"><SourceIcon kind={source.kind} hash={source.hash} size={17} /></span>

              <div class="source-main">
                <div class="source-title">
                  {source.title || source.hash.slice(0, 12)}
                  {#if source.bookmark}<span class="tag">link</span>{/if}
                  {#if source.sitemap}<span class="tag">sitemap</span>{/if}
                  {#if source.rss}<span class="tag">feed</span>{/if}
                  {#if source.origin === 'sitemap'}<span class="tag">sitemap page</span>{/if}
                  {#if source.origin === 'rss'}<span class="tag">feed item</span>{/if}
                  {#if source.from_feed}<span class="tag">feed copy</span>{/if}
                  {#if source.original_name?.startsWith('wiki:')}<span class="tag">wiki</span>{/if}
                  {#if source.origin === 'wiki'}<span class="tag">wiki URL</span>{/if}
                </div>
                <div class="source-sub">
                  <span>{source.kind} · {fmtBytes(source.bytes)} · {relTime(source.imported_at)}</span>
                  <span class="source-origin">{subLine(source)}</span>
                </div>
                {#if rowTagInfo.shown.length > 0}
                  <div class="source-tags">
                    {#each rowTagInfo.shown as item (item.tag)}
                      <span class="tag" class:tag--auto={item.auto}>{item.tag}</span>
                    {/each}
                    {#if rowTagInfo.more > 0}<span class="tag">+{rowTagInfo.more}</span>{/if}
                  </div>
                {/if}
              </div>

              <div class="source-status">
                <span class="source-label" class:working={!!source.running_step}>{statusLabel(source, live)}</span>
                {#if !source.bookmark}
                  <SourceSteps {source} {live} />
                {/if}
              </div>
            </button>

            <div class="source-actions">
              {#if source.rss || source.sitemap}
                <button
                  class="icon-btn"
                  title="Refresh — fetch the list again"
                  onclick={(event) => act(event, () => void refreshSource(source.hash))}
                >
                  <Icon name="refresh" size={14} />
                </button>
              {:else}
                <button
                  class="icon-btn"
                  title="Capture — read the source again"
                  onclick={(event) => act(event, () => void captureSource(source.hash))}
                >
                  <Icon name="capture" size={14} />
                </button>
                <button
                  class="icon-btn"
                  title="Rebuild the outline"
                  onclick={(event) => act(event, () => void rebuildSourceTree(source.hash))}
                >
                  <Icon name="tree" size={14} />
                </button>
                <button
                  class="icon-btn"
                  title="Distill — continue the claims of this source"
                  onclick={(event) => act(event, () => void distillSource(source.hash))}
                >
                  <Icon name="distill" size={14} />
                </button>
                <button
                  class="icon-btn"
                  title="Reset the derivation"
                  onclick={(event) => act(event, () => void resetSource(source.hash))}
                >
                  <Icon name="reset" size={14} />
                </button>
              {/if}
              {#if !source.origin && !source.original_name?.startsWith('wiki:')}
                <button
                  class="icon-btn icon-btn--danger"
                  title="Delete the source"
                  onclick={(event) => act(event, () => void deleteSource(source.hash))}
                >
                  <Icon name="trash" size={14} />
                </button>
              {/if}
            </div>
          </div>
        {/snippet}
      </VirtualList>
    </div>
  {/if}

  {#if system.notice}
    <p class="notice sources-notice">{system.notice}</p>
  {/if}
</div>

<style>
  .sources-page {
    padding-top: 24px;
  }

  .source-search {
    width: min(320px, 100%);
  }

  .source-list {
    border-top: 1px solid var(--line);
  }

  .source-row {
    position: relative;
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 78px;
    padding: 0 8px;
    border-bottom: 1px solid var(--line);
    transition: background 0.12s var(--ease);
  }

  .source-row:hover,
  .source-row:focus-within {
    background: var(--bg-hover);
  }

  .source-open {
    display: flex;
    align-items: center;
    gap: 12px;
    flex: 1;
    min-width: 0;
    padding: 9px 0;
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
  }

  .source-open:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
    border-radius: var(--radius-sm);
  }

  .source-icon {
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

  .source-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 2px;
  }

  .source-title {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    overflow: hidden;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .source-sub {
    display: flex;
    gap: 8px;
    min-width: 0;
    overflow: hidden;
    color: var(--faint);
    font-size: 11.5px;
    white-space: nowrap;
  }

  .source-tags {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
  }

  .tag--auto {
    opacity: 0.62;
  }

  .tag-cloud {
    display: flex;
    flex-wrap: wrap;
    gap: 5px;
    max-height: 74px;
    margin: 0 0 10px;
    overflow: auto;
    padding-bottom: 2px;
  }

  .tag-filter {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: -2px 0 10px;
    color: var(--faint);
    font-size: 11.5px;
  }

  .tag-filter b {
    color: var(--ink-2);
    font-weight: 550;
  }

  .link-button {
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--accent);
    font: inherit;
    cursor: pointer;
  }

  .source-origin {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .source-origin::before {
    content: "·";
    margin-right: 8px;
    color: var(--faint);
  }

  .source-status {
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 2px;
    min-width: 140px;
    flex: none;
    transition: opacity 0.12s var(--ease);
  }

  .source-label {
    max-width: 210px;
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .source-label.working {
    color: var(--info);
  }

  .source-actions {
    position: absolute;
    top: 50%;
    right: 8px;
    z-index: 1;
    display: flex;
    align-items: center;
    gap: 1px;
    border-radius: var(--radius-sm);
    background: var(--bg-hover);
    opacity: 0;
    pointer-events: none;
    transform: translateY(-50%);
    transition: opacity 0.12s var(--ease);
  }

  .source-row:hover .source-actions,
  .source-row:focus-within .source-actions {
    opacity: 1;
    pointer-events: auto;
  }

  .source-row:hover .source-status,
  .source-row:focus-within .source-status {
    opacity: 0;
  }

  .source-actions .icon-btn {
    width: 27px;
    height: 27px;
  }

  .sources-notice {
    margin-top: 14px;
  }

  @media (max-width: 860px) {
    .source-status {
      display: none;
    }

    .source-actions {
      position: static;
      opacity: 1;
      pointer-events: auto;
      padding-left: 0;
      background: transparent;
      transform: none;
    }
  }

  @media (max-width: 640px) {
    .source-origin {
      display: none;
    }

    .source-actions .icon-btn:nth-child(2),
    .source-actions .icon-btn:nth-child(4) {
      display: none;
    }
  }
</style>
