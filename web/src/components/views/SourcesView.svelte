<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { clearable } from '../../lib/clearable';
  import { deleteSource, toggleFavorite, toggleReadLater } from '../../lib/actions';
  import type { Source, SourceListFilter } from '../../lib/api/types';
  import { fmtBytes, relTime } from '../../lib/format';
  import { n, t } from '../../lib/i18n/index.svelte';
  import { openAdd, openInterests } from '../../lib/overlays';
  import { navigate, router, sourcePath, withQuery, withoutQuery } from '../../lib/router.svelte';
  import { connection } from '../../lib/stores/connection.svelte';
  import { sourceWindow } from '../../lib/stores/source-window.svelte';
  import { sources, type SourceKindFilter } from '../../lib/stores/sources.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { interests } from '../../lib/stores/interests.svelte';
  import { visibleCloud } from '../../lib/cloud';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { rememberSourcesTop } from '../../lib/sources-scroll';
  import { statusLabel } from '../../lib/source-state';
  import CloudMore from '../ui/CloudMore.svelte';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceLamp from '../sources/SourceLamp.svelte';
  import SourceSteps from '../sources/SourceSteps.svelte';
  import SourceWindowList from '../sources/SourceWindowList.svelte';
  import ListHead from '../ui/ListHead.svelte';
  import AssistantBanner from '../assistant/AssistantBanner.svelte';

  // The vault total comes from the status report, so the header is right from the first paint; the
  // list itself reads only the window around the viewport.
  const totalSources = $derived(system.status?.index.sources ?? sourceWindow.count);

  // The tag cloud is a FACET over the vault (human and derived labels, once per source), not a
  // count over the loaded rows.
  const tagCloud = $derived(sourceWindow.facets.tags);
  let tagsExpanded = $state(false);
  const tagView = $derived(
    visibleCloud(tagCloud, {
      expanded: tagsExpanded,
      active: sources.filter,
      key: (item) => item.tag,
      count: (item) => item.count,
    }),
  );
  const attachedCount = $derived(sourceWindow.facets.attached);

  // The filter is server-side: a page must be the page of the FILTERED list, or its count would lie
  // about the scroll height.
  const commandId = $derived(
    router.current.view === 'sources' ? (router.current.params.get('command') ?? '') : '',
  );
  // The reader's marks are NAMED filters, so they live in the address: the Start card's queue link,
  // a reload and another window all show the same list. The tag/kind/search filters stay in the
  // store, as they always have.
  const readLaterOnly = $derived(
    router.current.view === 'sources' && router.current.params.get('readlater') === '1',
  );
  const favoritesOnly = $derived(
    router.current.view === 'sources' && router.current.params.get('favorite') === '1',
  );
  const interestSlug = $derived(
    router.current.view === 'sources' ? (router.current.params.get('interest') ?? '') : '',
  );
  const activeFilter = $derived<SourceListFilter>({
    kind: sources.kind === 'all' ? '' : sources.kind,
    query: sources.query,
    tag: sources.filter,
    attachedOnly: sources.attachedOnly,
    // An assistant result is ONE filter dimension beside the named ones — the server reads the set
    // from the record, and the window keeps its count, pages and scroll height.
    command: commandId,
    favoriteOnly: favoritesOnly,
    readLaterOnly,
    interest: interestSlug,
  });
  const hasFilter = $derived(
    !!sources.query.trim() ||
      sources.kind !== 'all' ||
      !!sources.filter ||
      sources.attachedOnly ||
      favoritesOnly ||
      readLaterOnly,
  );

  // An assistant result arrives as an address, and the address is the truth about what is shown: the
  // controls are SEEDED from it, so the visible filter bar cannot disagree with the list. Free text
  // never appears here — it lives in the record, not in the URL.
  $effect(() => {
    const id = commandId;
    if (!id) return;
    untrack(() => {
      const params = router.current.params;
      const kind = params.get('kind') ?? '';
      sources.kind = (['md', 'pdf', 'txt', 'epub', 'url'].includes(kind) ? kind : 'all') as SourceKindFilter;
      sources.filter = params.get('tag') ?? '';
      sources.attachedOnly = params.get('attached') === '1';
      sources.query = '';
    });
  });

  // Seed the window with the filter that survived a trip into a source; the effect below only has
  // to react to later changes.
  untrack(() =>
    sourceWindow.setFilter({
      kind: sources.kind === 'all' ? '' : sources.kind,
      query: sources.query,
      tag: sources.filter,
      attachedOnly: sources.attachedOnly,
      command: router.current.params.get('command') ?? '',
      favoriteOnly: router.current.params.get('favorite') === '1',
      readLaterOnly: router.current.params.get('readlater') === '1',
      interest: router.current.params.get('interest') ?? '',
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
    if (!interests.loaded) untrack(() => void interests.load());
  });

  function toggleParam(key: string): void {
    if (router.current.params.get(key) === '1') {
      // Leaving the queue also drops its per-interest slice: a queue filter without a queue is a
      // different list that happens to be ordered the same way.
      navigate(key === 'readlater' ? withoutQuery('readlater', 'interest') : withoutQuery(key));
      return;
    }
    navigate(withQuery(key, '1'));
  }

  function interestSlugs(source: Source): string[] {
    // A rule the reader deleted leaves a dangling slug in the record; it is not rendered. The queue
    // entry itself is theirs and stays.
    return (source.read_later_via ?? []).filter((slug) => !!interests.bySlug(slug));
  }

  function interestNames(source: Source): string[] {
    return interestSlugs(source).map((slug) => interests.nameOf(slug));
  }

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

    {#if commandId}
      <AssistantBanner
        id={commandId}
        expired={sourceWindow.commandRef?.expired ?? false}
        total={sourceWindow.commandRef?.total ?? sourceWindow.count}
        missing={sourceWindow.commandRef?.missing ?? 0}
        lens="sources"
      />
    {/if}

    {#if readLaterOnly}
      <section class="queue-banner" aria-label={t('sources.queueTitle')}>
        <div class="queue-head">
          <Icon name="clock" size={15} />
          <strong>{t('sources.queueTitle')}</strong>
          <span class="queue-count">{n(sourceWindow.count)}</span>
          {#if !interestSlug}
            <span class="queue-split">
              {t('sources.queueSplit', {
                from: n(sourceWindow.facets.read_later_interests),
                self: n(Math.max(0, sourceWindow.facets.read_later - sourceWindow.facets.read_later_interests)),
              })}
            </span>
          {/if}
          <button class="btn btn--ghost btn--sm queue-manage" onclick={() => openInterests()}>
            <Icon name="target" size={13} /> <span class="btn-text">{t('interests.manage')}</span>
          </button>
        </div>
        <div class="queue-chips">
          <button
            class="chip"
            class:chip--accent={!interestSlug}
            onclick={() => navigate(withoutQuery('interest'))}
          >
            {t('sources.queueAll')}<span class="chip-count">{n(sourceWindow.facets.read_later)}</span>
          </button>
          {#each interests.items.filter((rule) => rule.open > 0) as rule (rule.slug)}
            <button
              class="chip"
              class:chip--accent={interestSlug === rule.slug}
              title={rule.terms.join(', ')}
              onclick={() => navigate(withQuery('interest', rule.slug))}
            >
              <Icon name="target" size={11} /> {rule.name}<span class="chip-count">{n(rule.open)}</span>
            </button>
          {/each}
        </div>
      </section>
    {/if}

    <div class="search-wrap list-search">
      <Icon name="search" size={14} />
      <input
        use:clearable
        class="input input--search"
        placeholder={t('sources.filterPlaceholder')}
        bind:value={sources.query}
        oninput={() => {
          // Typing is the human taking the filter back: an assistant result is not a box to type
          // over, so the command leaves the address and the words apply to the whole vault.
          if (commandId) navigate(withoutQuery('command'));
        }}
        aria-label={t('sources.filterAria')}
      />
    </div>

    <div class="list-filters">
      <div class="seg" role="tablist" aria-label={t('sources.kindAria')}>
        <button class:on={sources.kind === 'all'} onclick={() => setKind('all')}>{t('sources.kindAll')}</button>
        <button class:on={sources.kind === 'md'} onclick={() => setKind('md')}>{t('sources.kindMarkdown')}</button>
        <button class:on={sources.kind === 'pdf'} onclick={() => setKind('pdf')}>{t('sources.kindPdf')}</button>
        <button class:on={sources.kind === 'epub'} onclick={() => setKind('epub')}>{t('sources.kindEpub')}</button>
        <button class:on={sources.kind === 'txt'} onclick={() => setKind('txt')}>{t('sources.kindText')}</button>
        <button class:on={sources.kind === 'url'} onclick={() => setKind('url')}>{t('sources.kindUrls')}</button>
      </div>
      {#if readLaterOnly || sourceWindow.facets.read_later > 0}
        <button
          class="chip attached-filter"
          class:chip--accent={readLaterOnly}
          aria-pressed={readLaterOnly}
          title={t('sources.readLaterTitle')}
          onclick={() => toggleParam('readlater')}
        >
          <Icon name="clock" size={12} /> {t('sources.readLater')}<span class="chip-count">{n(sourceWindow.facets.read_later)}</span>
        </button>
      {/if}
      {#if favoritesOnly || sourceWindow.facets.favorites > 0}
        <button
          class="chip attached-filter"
          class:chip--accent={favoritesOnly}
          aria-pressed={favoritesOnly}
          title={t('sources.favoritesTitle')}
          onclick={() => toggleParam('favorite')}
        >
          <Icon name="star" size={12} /> {t('sources.favorites')}<span class="chip-count">{n(sourceWindow.facets.favorites)}</span>
        </button>
      {/if}
      <button class="chip attached-filter" title={t('interests.manageTitle')} onclick={() => openInterests()}>
        <Icon name="target" size={12} /> {t('interests.manage')}
      </button>
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
        {#each tagView.visible as tag (tag.tag)}
          <button
            class="chip"
            class:chip--accent={sources.filter === tag.tag}
            onclick={() => (sources.filter = sources.filter === tag.tag ? '' : tag.tag)}
          >
            {tag.tag}<span class="chip-count">{n(tag.count)}</span>
          </button>
        {/each}
      </div>
      <CloudMore
        hidden={tagView.hidden}
        expanded={tagsExpanded}
        truncated={tagView.truncated}
        ontoggle={() => (tagsExpanded = !tagsExpanded)}
      />
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
      {#if readLaterOnly && !interestSlug}
        <EmptyState icon="clock" title={t('sources.queueEmptyTitle')} text={t('sources.queueEmptyText')}>
          <button class="btn btn--secondary btn--sm" onclick={() => openInterests()}>
            <Icon name="target" size={13} /> {t('home.readLaterEmptyAction')}
          </button>
        </EmptyState>
      {:else if hasFilter}
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
                    {#if source.favorite_at || source.read_later}
                      <span class="source-marks">
                        {#if source.favorite_at}
                          <span class="source-mark" title={t('sources.favorite')}><Icon name="star" size={12} /></span>
                        {/if}
                        {#if source.read_later}
                          <span class="source-mark" title={t('sources.readLater')}><Icon name="clock" size={12} /></span>
                        {/if}
                      </span>
                    {/if}
                    <span class="source-name truncate" title={source.title || source.hash.slice(0, 12)}>{source.title || source.hash.slice(0, 12)}</span>
                    {#if source.bookmark}<span class="tag">{t('sources.tagLink')}</span>{/if}
                    {#if source.sitemap}<span class="tag">{t('sources.tagSitemap')}</span>{/if}
                    {#if source.rss}<span class="tag">{t('sources.tagFeed')}</span>{/if}
                    {#if source.origin === 'sitemap'}<span class="tag">{t('sources.tagSitemapPage')}</span>{/if}
                    {#if source.origin === 'rss'}
                      <span class="tag tag--wide">{t('sources.tagFeedItem')}</span>
                      <span class="tag tag--narrow" title={t('sources.tagFeedItem')}>{t('sources.tagFeedItemShort')}</span>
                    {/if}
                    {#if source.from_feed}
                      <span class="tag tag--wide">{t('sources.tagFeedCopy')}</span>
                      <span class="tag tag--narrow" title={t('sources.tagFeedCopy')}>{t('sources.tagFeedCopyShort')}</span>
                    {/if}
                    {#if source.original_name?.startsWith('wiki:')}<span class="tag">{t('sources.tagWiki')}</span>{/if}
                    {#if source.origin === 'wiki'}<span class="tag">{t('sources.tagWikiUrl')}</span>{/if}
                  </div>
                  <div class="source-sub">
                    <span>{source.kind} · {fmtBytes(source.bytes)} · {relTime(source.imported_at)}</span>
                    <span class="source-origin">{subLine(source)}</span>
                  </div>
                  {#if (source.read_later && interestSlugs(source).length > 0) || rowTagInfo.shown.length > 0}
                    <div class="source-tags">
                      {#if source.read_later && interestSlugs(source).length > 0}
                        <span
                          class="tag tag--interest"
                          title={interestNames(source).join(', ')}
                        >
                          <Icon name="target" size={10} /> {interestNames(source)[0]}{#if interestSlugs(source).length > 1}+{interestSlugs(source).length - 1}{/if}
                        </span>
                      {/if}
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
                <button
                  class="icon-btn"
                  class:is-on={source.read_later}
                  title={source.read_later ? t('sources.markDone') : t('sources.readLaterAction')}
                  aria-pressed={!!source.read_later}
                  onclick={(event) => act(event, () => void toggleReadLater(source.hash, !source.read_later))}
                >
                  <Icon name="clock" size={14} />
                </button>
                <button
                  class="icon-btn"
                  class:is-on={!!source.favorite_at}
                  title={source.favorite_at ? t('sources.unfavorite') : t('sources.favorite')}
                  aria-pressed={!!source.favorite_at}
                  onclick={(event) => act(event, () => void toggleFavorite(source.hash, !source.favorite_at))}
                >
                  <Icon name="star" size={14} />
                </button>
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

  /* The two feed labels are long enough to eat the title on a narrow row: the list carries a
     shortened variant there, with the full label in the tooltip. */
  .source-title > .tag--narrow {
    display: none;
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

  @media (max-width: 1000px) {
    /* A narrow window: use the shortened feed labels, with the full label in the tooltip. */
    .source-title > .tag--wide {
      display: none;
    }

    .source-title > .tag--narrow {
      display: inline-flex;
    }
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

  /* The read-later queue: the same list, told what it is. It sits above the search because the
     queue is a place the reader comes back to, not a filter they happened to switch on. */
  .queue-banner {
    /* The gap to the search below is the same 10px the search keeps to the filter row. */
    margin: 12px 0 10px;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
  }

  .queue-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    color: var(--ink);
    font-size: 12.5px;
  }

  .queue-head strong {
    font-weight: 650;
  }

  .queue-count {
    color: var(--faint);
    font-variant-numeric: tabular-nums;
  }

  .queue-split {
    color: var(--muted);
    font-size: 11.5px;
  }

  .queue-manage {
    margin-left: auto;
  }

  .queue-chips {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin-top: 9px;
  }

  .source-marks {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    flex: none;
    color: var(--accent);
  }

  .source-mark {
    display: inline-flex;
    align-items: center;
  }

  .tag--interest {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent);
  }

  :global(.source-actions .icon-btn.is-on) {
    color: var(--accent);
    background: var(--accent-soft);
  }
</style>
