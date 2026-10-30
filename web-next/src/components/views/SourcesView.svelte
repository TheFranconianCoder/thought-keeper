<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { clearable } from '../../lib/clearable';
  import { captureSource, deleteSource, distillSource, openSourceFromHash, rebuildSourceTree, refreshSource, resetSource } from '../../lib/actions';
  import type { Source, SourceListFilter } from '../../lib/api/types';
  import { fmtBytes, relTime } from '../../lib/format';
  import { n, t } from '../../lib/i18n/index.svelte';
  import { openAdd } from '../../lib/overlays';
  import { router } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { sourceWindow } from '../../lib/stores/source-window.svelte';
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
  import SourceLamp from '../sources/SourceLamp.svelte';
  import SourceSteps from '../sources/SourceSteps.svelte';
  import SourceWindowList from '../sources/SourceWindowList.svelte';
  import ListHead from '../ui/ListHead.svelte';

  // The vault total comes from the status report, so the header is right from the first paint; the
  // list itself reads only the window around the viewport.
  const totalSources = $derived(system.status?.index.sources ?? sourceWindow.count);

  // The tag cloud is a FACET over the vault (human and derived labels, once per source), not a
  // count over the loaded rows.
  const tagCloud = $derived(sourceWindow.facets.tags);
  const attachedCount = $derived(sourceWindow.facets.attached);

  // The filter is server-side: a page must be the page of the FILTERED list, or its count would lie
  // about the scroll height.
  const activeFilter = $derived<SourceListFilter>({
    kind: sources.kind === 'all' ? '' : sources.kind,
    query: sources.query,
    tag: sources.filter,
    attachedOnly: sources.attachedOnly,
  });
  const hasFilter = $derived(
    !!sources.query.trim() || sources.kind !== 'all' || !!sources.filter || sources.attachedOnly,
  );

  // Seed the window with the filter that survived a trip into a source; the effect below only has
  // to react to later changes.
  untrack(() =>
    sourceWindow.setFilter({
      kind: sources.kind === 'all' ? '' : sources.kind,
      query: sources.query,
      tag: sources.filter,
      attachedOnly: sources.attachedOnly,
    }),
  );
  // The first page must be read even before the total is known: the list cannot ask for a range
  // while it does not know how tall it is. The epoch effect below does that on mount and after a
  // reset; the list handles the visible range once a count exists.

  let filterTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const filter = activeFilter;
    clearTimeout(filterTimer);
    // Typing a search should not fire one window read per keystroke.
    filterTimer = setTimeout(() => {
      sourceWindow.setFilter(filter);
    }, 100);
    return () => clearTimeout(filterTimer);
  });

  $effect(() => {
    // A reset (filter, source event) discards the pages. When the list has a count, its own window
    // asks for the visible range; only an empty/not-yet-known list needs the first page here.
    void sourceWindow.epoch;
    if (!sourceWindow.loaded || sourceWindow.count === 0) {
      untrack(() => void sourceWindow.ensure(0, 30));
    }
  });

  $effect(() => {
    if (router.current.view !== 'sources') return;
    if (!sourceWindow.facetsLoaded) untrack(() => void sourceWindow.loadFacets());
  });

  function setKind(next: SourceKindFilter): void {
    sources.kind = next;
  }

  function retry(): void {
    sourceWindow.reset();
    void sourceWindow.loadFacets();
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
  <ListHead
    title={t('view.sources')}
    count={sourceWindow.loaded || totalSources > 0 ? totalSources : null}
  >
    {#snippet action()}
      <button class="btn btn--primary" onclick={openAdd}>
        <Icon name="plus" size={14} /> <span class="btn-text">{t('nav.addSource')}</span>
      </button>
    {/snippet}

    <div class="search-wrap list-search">
      <Icon name="search" size={14} />
      <input
        use:clearable
        class="input input--search"
        placeholder={t('sources.filterPlaceholder')}
        bind:value={sources.query}
        aria-label={t('sources.filterAria')}
      />
    </div>

    <div class="list-filters">
      <div class="seg" role="tablist" aria-label={t('sources.kindAria')}>
        <button class:on={sources.kind === 'all'} onclick={() => setKind('all')}>{t('sources.kindAll')}</button>
        <button class:on={sources.kind === 'md'} onclick={() => setKind('md')}>{t('sources.kindMarkdown')}</button>
        <button class:on={sources.kind === 'pdf'} onclick={() => setKind('pdf')}>{t('sources.kindPdf')}</button>
        <button class:on={sources.kind === 'txt'} onclick={() => setKind('txt')}>{t('sources.kindText')}</button>
        <button class:on={sources.kind === 'url'} onclick={() => setKind('url')}>{t('sources.kindUrls')}</button>
      </div>
      {#if attachedCount > 0 || sources.attachedOnly}
        <button
          class="chip attached-filter"
          class:chip--warn={sources.attachedOnly}
          aria-pressed={sources.attachedOnly}
          title={t('sources.attachedTitle')}
          onclick={() => (sources.attachedOnly = !sources.attachedOnly)}
        >
          <Icon name="paperclip" size={12} /> {t('sources.attached')}<span class="chip-count">{n(attachedCount)}</span>
        </button>
      {/if}
    </div>

    {#if tagCloud.length > 0}
      <div
        class="chip-cloud"
        use:scrollFade
        use:rememberScroll={'source-tags'}
        aria-label={t('sources.tagsAria')}
      >
        {#each tagCloud as tag (tag.tag)}
          <button
            class="chip"
            class:chip--accent={sources.filter === tag.tag}
            onclick={() => (sources.filter = sources.filter === tag.tag ? '' : tag.tag)}
          >
            {tag.tag}<span class="chip-count">{n(tag.count)}</span>
          </button>
        {/each}
      </div>
    {/if}
    {#if sources.filter}
      <div class="filter-line">
        {t('sources.filteredBy')} <b>{sources.filter}</b>
        <button class="link-button" onclick={() => (sources.filter = '')}>{t('common.clear')}</button>
      </div>
    {/if}
  </ListHead>

  {#if sourceWindow.error}
    <p class="notice notice--error">
      {sourceWindow.error}
      <button class="btn btn--ghost btn--sm" onclick={retry}>{t('common.retry')}</button>
    </p>
  {/if}
  {#if !sourceWindow.loaded || (sourceWindow.loading && sourceWindow.count === 0)}
    {#if connection.state !== 'live'}
      <EmptyState icon="sources" title={t('sources.nothingLoadedTitle')} text={t('sources.nothingLoadedText')} />
    {:else}
      <div class="stack">
        {#each Array(5) as _, index (index)}<span class="skeleton" style="height:52px"></span>{/each}
      </div>
    {/if}
  {:else if sourceWindow.count === 0}
    {#if !sourceWindow.error}
      {#if hasFilter}
        <EmptyState icon="filter" title={t('sources.noMatchTitle')} text={t('sources.noMatchText')} />
      {:else}
        <EmptyState
          icon="sources"
          title={t('sources.emptyTitle')}
          text={t('sources.emptyText')}
        >
          <button class="btn btn--primary btn--sm" onclick={openAdd}>
            <Icon name="plus" size={13} /> {t('sources.addFirst')}
          </button>
        </EmptyState>
      {/if}
    {/if}
  {:else}
    <div class="source-list">
      <SourceWindowList
        count={sourceWindow.count}
        rowHeight={78}
        itemAt={(index) => sourceWindow.itemAt(index)}
        ensure={(start, end) => void sourceWindow.ensure(start, end)}
        epoch={sourceWindow.epoch}
      >
        {#snippet row(source: Source | undefined)}
          {#if source}
            {@const live = sources.progress[source.hash]}
            {@const rowTagInfo = rowTags(source)}
            <div class="source-row">
              <button
                class="source-open"
                title={statusLabel(source, live)}
                onclick={() => open(source)}
              >
                <span class="source-icon"><SourceIcon kind={source.kind} hash={source.hash} size={17} /></span>

                <div class="source-main">
                  <div class="source-title">
                    {#if source.attached}
                      <span
                        class="tag tag--attached"
                        title={source.attached_from
                          ? t('sources.attachedTagTitleFrom', { from: source.attached_from })
                          : t('sources.attachedTagTitle')}
                      >
                        <Icon name="paperclip" size={10} /> {t('sources.attachedTag')}
                      </span>
                    {/if}
                    <span class="source-name truncate" title={source.title || source.hash.slice(0, 12)}>{source.title || source.hash.slice(0, 12)}</span>
                    {#if source.bookmark}<span class="tag">{t('sources.tagLink')}</span>{/if}
                    {#if source.sitemap}<span class="tag">{t('sources.tagSitemap')}</span>{/if}
                    {#if source.rss}<span class="tag">{t('sources.tagFeed')}</span>{/if}
                    {#if source.origin === 'sitemap'}<span class="tag">{t('sources.tagSitemapPage')}</span>{/if}
                    {#if source.origin === 'rss'}<span class="tag">{t('sources.tagFeedItem')}</span>{/if}
                    {#if source.from_feed}<span class="tag">{t('sources.tagFeedCopy')}</span>{/if}
                    {#if source.original_name?.startsWith('wiki:')}<span class="tag">{t('sources.tagWiki')}</span>{/if}
                    {#if source.origin === 'wiki'}<span class="tag">{t('sources.tagWikiUrl')}</span>{/if}
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
                <span class="source-lamp"><SourceLamp {source} {live} /></span>
              </button>

              <div class="source-actions">
                {#if source.rss || source.sitemap}
                  <button
                    class="icon-btn"
                    title={t('sources.actionRefresh')}
                    onclick={(event) => act(event, () => void refreshSource(source.hash))}
                  >
                    <Icon name="refresh" size={14} />
                  </button>
                {:else}
                  <button
                    class="icon-btn"
                    title={t('sources.actionCapture')}
                    onclick={(event) => act(event, () => void captureSource(source.hash))}
                  >
                    <Icon name="capture" size={14} />
                  </button>
                  <button
                    class="icon-btn"
                    title={t('sources.actionOutline')}
                    onclick={(event) => act(event, () => void rebuildSourceTree(source.hash))}
                  >
                    <Icon name="tree" size={14} />
                  </button>
                  <button
                    class="icon-btn"
                    title={t('sources.actionDistill')}
                    onclick={(event) => act(event, () => void distillSource(source.hash))}
                  >
                    <Icon name="distill" size={14} />
                  </button>
                  <button
                    class="icon-btn"
                    title={t('sources.actionReset')}
                    onclick={(event) => act(event, () => void resetSource(source.hash))}
                  >
                    <Icon name="reset" size={14} />
                  </button>
                {/if}
                {#if !source.origin && !source.original_name?.startsWith('wiki:')}
                  <button
                    class="icon-btn icon-btn--danger"
                    title={t('sources.actionDelete')}
                    onclick={(event) => act(event, () => void deleteSource(source.hash))}
                  >
                    <Icon name="trash" size={14} />
                  </button>
                {/if}
              </div>
            </div>
          {:else}
            <div class="source-row source-row--skeleton" aria-hidden="true">
              <span class="skeleton skeleton-row"></span>
            </div>
          {/if}
        {/snippet}
      </SourceWindowList>
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

  .source-row--skeleton {
    padding: 0 9px;
  }

  .source-row--skeleton:hover {
    background: transparent;
  }

  .skeleton-row {
    width: 100%;
    height: 56px;
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
    white-space: nowrap;
  }

  .source-title > .tag {
    flex: none;
  }

  .source-name {
    flex: 1 1 auto;
    min-width: 0;
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

  .source-sub > span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .source-sub > span:first-child {
    flex: none;
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

  .tag--attached {
    flex: none;
    gap: 3px;
    background: var(--warning-soft);
    color: var(--warning);
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

  .source-lamp {
    display: none;
    flex: none;
  }

  @media (max-width: 860px) {
    /* Narrow list: the four step lights, the status line and the row actions are the wrong
       information density. One lamp shows the whole derivation; the detail page carries the
       actions, icon-only. */
    .source-status {
      display: none;
    }

    .source-actions {
      display: none;
    }

    .source-lamp {
      display: inline-flex;
      margin-right: 2px;
    }
  }

  @media (max-width: 640px) {
    .source-origin {
      display: none;
    }
  }
</style>
