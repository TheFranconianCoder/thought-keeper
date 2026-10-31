<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { onDestroy, untrack } from 'svelte';
  import { api } from '../../lib/api';
  import { humanError } from '../../lib/api/errors';
  import type { Atom, SearchHit } from '../../lib/api/types';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { claimPath, claimTopicPath, navigate, router, sourcePath } from '../../lib/router.svelte';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { scrollTopTarget } from '../../lib/scroll-top.svelte';
  import { CLAIM_PAGE_SIZE, claims } from '../../lib/stores/claims.svelte';
  import { visibleCloud } from '../../lib/cloud';
  import { connection } from '../../lib/stores/connection.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import CloudMore from '../ui/CloudMore.svelte';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import Markdown from '../ui/Markdown.svelte';
  import CopyId from '../ui/CopyId.svelte';
  import ListHead from '../ui/ListHead.svelte';
  import VirtualList from '../ui/VirtualList.svelte';
  import AssistantBanner from '../assistant/AssistantBanner.svelte';

  const detailOpen = $derived(router.current.view === 'claims' && !!router.current.id);
  const topic = $derived(router.current.view === 'claims' ? (router.current.params.get('topic') ?? '') : '');
  const commandId = $derived(
    router.current.view === 'claims' ? (router.current.params.get('command') ?? '') : '',
  );

  let topicsExpanded = $state(false);
  const topicView = $derived(
    visibleCloud(claims.topics, {
      expanded: topicsExpanded,
      active: topic,
      key: (item) => item.topic,
      count: (item) => item.count,
    }),
  );

  let query = $state('');
  let results = $state<SearchHit[]>([]);
  let searching = $state(false);
  let searchError = $state('');
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  let searchSeq = 0;
  let lastDeepLinkQuery = '';

  const searchMode = $derived(query.trim().length >= 2);
  const topicNames = $derived(claims.topics.map((item) => item.topic));

  // The list endpoint returns only the slice, so `claims.total` is the slice length, not the vault
  // total. The status report has the real count; a topic's count comes from the full topic catalogue.
  const total = $derived.by(() => {
    if (topic) return claims.topicCounts[topic] ?? claims.total;
    return system.status?.index.atoms ?? claims.total;
  });

  const caption = $derived.by(() => {
    if (searchMode) {
      return searching
        ? t('claims.searching')
        : tn('claims.resultCount', results.length, { query: query.trim() });
    }
    const all = total <= CLAIM_PAGE_SIZE;
    if (topic) {
      return all
        ? tn('claims.captionTopicAll', total)
        : tn('claims.captionNewestTopic', total, { pageSize: CLAIM_PAGE_SIZE });
    }
    return all
      ? tn('claims.captionAll', total)
      : tn('claims.captionNewest', total, { pageSize: CLAIM_PAGE_SIZE });
  });

  $effect(() => {
    const route = router.current;
    if (route.view !== 'claims') return;

    const nextTopic = route.params.get('topic') ?? '';
    // An assistant result in the Claims lens is the materialized set of its record, not the topic
    // filter: the address names the command, and the list reads its members.
    const nextCommand = route.params.get('command') ?? '';
    // Store methods read their own reactive fields. They must stay untracked: an effect that
    // depended on them would rerun after every load and reload the same container forever.
    untrack(() => {
      if (
        nextTopic !== claims.topic ||
        nextCommand !== claims.command ||
        !claims.loaded ||
        claims.stale
      ) {
        void claims.load(nextTopic, nextCommand);
      }
      if (claims.topics.length === 0 || claims.topicsStale) void claims.loadTopics();

      if (route.id) void claims.open(route.id);
      else claims.clearSelection();
    });

    const q = route.params.get('q') ?? '';
    if (q !== lastDeepLinkQuery) {
      lastDeepLinkQuery = q;
      query = q;
      if (q.trim().length >= 2) void runSearch(q.trim());
      else {
        results = [];
        searching = false;
      }
    }
  });

  // The reader's place in the list belongs to the list; opening a claim must not move it. The detail
  // pane's position is restored by the same history-entry memory as the list.
  onDestroy(() => clearTimeout(searchTimer));

  async function runSearch(term: string): Promise<void> {
    const seq = ++searchSeq;
    searching = true;
    searchError = '';
    try {
      const result = await api.search(term, 'atoms', 50);
      if (seq !== searchSeq) return;
      results = result.hits ?? [];
    } catch (error) {
      if (seq !== searchSeq) return;
      results = [];
      searchError = humanError(error);
    } finally {
      if (seq === searchSeq) searching = false;
    }
  }

  function onSearchInput(value: string): void {
    query = value;
    clearTimeout(searchTimer);
    const term = value.trim();
    if (term.length < 2) {
      results = [];
      searching = false;
      searchError = '';
      return;
    }
    searchTimer = setTimeout(() => void runSearch(term), 170);
  }

  function openClaim(id: string): void {
    // The list's topic filter travels with the claim: the detail view keeps the filtered list on the
    // left, and "back" returns to it.
    navigate(claimPath(id, topic));
  }

  function openSource(hash: string, node?: string): void {
    navigate(sourcePath(hash, node ? { tab: 'snapshot', part: node } : {}));
  }
</script>

<div class="page claims-page" class:split={detailOpen}>
  <section class="claims-list-pane" aria-label={t('view.claims')}>
    <ListHead title={t('view.claims')} count={total}>
      {#if commandId}
        <AssistantBanner
          id={commandId}
          expired={claims.commandRef?.expired ?? false}
          total={claims.commandRef?.total ?? claims.total}
          missing={claims.commandRef?.missing ?? 0}
          lens="claims"
        />
      {/if}
      <div class="search-wrap list-search">
        <Icon name="search" size={14} />
        <input
          use:clearable
          class="input input--search"
          placeholder={t('claims.searchPlaceholder')}
          value={query}
          oninput={(event) => onSearchInput(event.currentTarget.value)}
          autocomplete="off"
          spellcheck="false"
          aria-label={t('claims.searchAria')}
        />
      </div>
      {#if !searchMode && claims.topics.length > 0}
        <div
          class="chip-cloud"
          use:scrollFade
          use:rememberScroll={'claims-topics'}
          aria-label={t('claims.topicsAria')}
        >
          {#each topicView.visible as item (item.topic)}
            <a
              class="chip"
              class:chip--accent={topic === item.topic}
              href={topic === item.topic ? '#/claims' : claimTopicPath(item.topic)}
            >
              {item.topic}<span class="chip-count">{n(item.count)}</span>
            </a>
          {/each}
        </div>
        <CloudMore
          hidden={topicView.hidden}
          expanded={topicsExpanded}
          truncated={topicView.truncated}
          ontoggle={() => (topicsExpanded = !topicsExpanded)}
        />
      {/if}
      {#if topic && !searchMode}
        <div class="filter-line">
          <span class="muted">{t('claims.filteredBy')}</span>
          <a class="chip chip--accent" href="#/claims">{topic}<Icon name="close" size={10} /></a>
        </div>
      {/if}
      <div class="list-caption">{caption}</div>
    </ListHead>

    <div class="claim-rows" use:scrollFade use:rememberScroll={'claims-list'} use:scrollTopTarget>
      {#if searchMode}
        {#if searchError}
          <p class="notice notice--error">{searchError}</p>
        {:else if !searching && results.length === 0}
          <EmptyState
            icon="search"
            title={t('claims.noMatchesTitle')}
            text={t('claims.noMatchesText', { query: query.trim() })}
          />
        {:else}
          {#each results as hit (hit.id)}
            <button class="claim-item" onclick={() => openClaim(hit.id)}>
              <span class="claim-text">{hit.title}</span>
              <span class="claim-sub">{hit.snippet}</span>
            </button>
          {/each}
        {/if}
      {:else if claims.error}
        <p class="notice notice--error">{claims.error}</p>
      {:else if !claims.loaded}
        {#if connection.state !== 'live'}
          <EmptyState
            icon="claims"
            title={t('claims.notLoadedTitle')}
            text={t('claims.notLoadedText')}
          />
        {:else}
          <div class="stack">
            {#each Array(4) as _, index (index)}<span class="skeleton" style="height:42px"></span>{/each}
          </div>
        {/if}
      {:else if claims.items.length === 0}
        <EmptyState
          icon="claims"
          title={topic ? t('claims.emptyTopicTitle') : t('claims.emptyTitle')}
          text={topic ? t('claims.emptyTopicText') : t('claims.emptyText')}
        />
      {:else}
        <VirtualList items={claims.items} rowHeight={58} key={(claim) => claim.id}>
          {#snippet row(claim: Atom)}
            <button
              class="claim-item"
              class:active={claims.selected?.id === claim.id}
              onclick={() => openClaim(claim.id)}
            >
              <span class="claim-text">{claim.claim}</span>
              <span class="claim-sub">
                {claim.topic}{claim.type ? ` · ${claim.type}` : ''}{claim.cluster_count
                  ? ` · ${tn('claims.clusterCount', claim.cluster_count)}`
                  : ''}
              </span>
            </button>
          {/snippet}
        </VirtualList>
      {/if}
    </div>
  </section>

  {#if detailOpen}
    <article class="claim-detail detail-frame" aria-label={t('claims.detailAria')}>
      <a class="back-link" href={topic ? claimTopicPath(topic) : '#/claims'}>{t('claims.back')}</a>

      {#if claims.detailError}
        <p class="notice notice--error">{claims.detailError}</p>
      {:else if claims.detailLoading && !claims.selected}
        <div class="stack">
          <span class="skeleton" style="height:18px;width:60px"></span>
          <span class="skeleton" style="height:54px"></span>
          <span class="skeleton" style="height:120px"></span>
        </div>
      {:else if claims.selected}
        {@const claim = claims.selected}
        <div class="kicker">{t('claims.kicker')}</div>
        <h2 class="statement">{claim.claim}</h2>

        <div class="claim-meta">
          {#if claim.topic}
            <a class="chip chip--accent" href={claimTopicPath(claim.topic)}>{claim.topic}</a>
          {/if}
          {#if claim.type}<span class="chip">{claim.type}</span>{/if}
          {#if claim.status}<span class="chip">{claim.status}</span>{/if}
          {#if claim.cluster_count}
            <span class="chip">{tn('claims.clusterCount', claim.cluster_count)}</span>
          {/if}
          <span class="spacer"></span>
          <CopyId text={claim.id} label={t('claims.idLabel')} display={claim.id.slice(0, 10)} />
        </div>

        {#if claim.body}
          <div class="claim-body">
            <Markdown text={claim.body} topics={topicNames} claimTopic={topic} wikilinks={false} />
          </div>
        {/if}

        {#if claim.sources?.length}
          <section class="detail-section">
            <div class="section-head">
              <h3>{t('claims.provenance')}</h3>
              <span class="count">{n(claim.sources.length)}</span>
            </div>
            <div class="provenance">
              {#each claim.sources as source (source.hash + ':' + (source.node ?? ''))}
                <button class="provenance-row" onclick={() => openSource(source.hash, source.node)}>
                  <Icon name="sources" size={14} />
                  <span class="provenance-main">
                    <span class="provenance-title">{source.title || source.hash.slice(0, 16)}</span>
                    <span class="provenance-sub">
                      {t('claims.node', { node: source.node || t('common.none') })}{source.ocr
                        ? ` · ${t('claims.ocr')}`
                        : ''}
                    </span>
                  </span>
                  <Icon name="arrowUpRight" size={13} />
                </button>
              {/each}
            </div>
          </section>
        {/if}

        <section class="detail-section">
          <div class="section-head">
            <h3>{t('claims.peers')}</h3>
            <span class="count">{n(claims.relatedPeers.length)}</span>
          </div>
          {#if claims.relatedPeers.length > 0}
            <div class="related">
              {#each claims.relatedPeers as related (related.id)}
                <button class="related-row" onclick={() => openClaim(related.id)}>
                  <Icon name="claims" size={14} />
                  <span class="related-main">
                    <span class="related-text">{related.claim}</span>
                    <span class="related-sub">{related.topic}</span>
                  </span>
                </button>
              {/each}
            </div>
          {:else}
            <p class="section-note">{t('claims.noPeers')}</p>
          {/if}
        </section>

        <section class="detail-section">
          <div class="section-head">
            <h3>{t('claims.sameTopic')}</h3>
            <span class="count">{n(claims.relatedSameTopic.length)}</span>
          </div>
          {#if claims.relatedSameTopic.length > 0}
            <div class="related">
              {#each claims.relatedSameTopic as related (related.id)}
                <button class="related-row" onclick={() => openClaim(related.id)}>
                  <Icon name="claims" size={14} />
                  <span class="related-main">
                    <span class="related-text">{related.claim}</span>
                    <span class="related-sub">{related.topic}</span>
                  </span>
                </button>
              {/each}
            </div>
          {:else}
            <p class="section-note">
              {claim.topic
                ? t('claims.noSameTopicTopic', { topic: claim.topic })
                : t('claims.noSameTopic')}
            </p>
          {/if}
        </section>
      {/if}
    </article>
  {/if}
</div>

<style>
  .claims-page {
    padding-top: 24px;
  }

  .claims-page.split {
    display: grid;
    grid-template-columns: minmax(280px, 360px) minmax(0, 1fr);
    gap: 38px;
    align-items: start;
    height: auto;
    min-height: calc(100dvh - var(--topbar-h));
    max-width: 1280px;
    padding-top: 0;
    padding-bottom: 0;
  }

  .claims-list-pane {
    min-width: 0;
  }

  .split .claims-list-pane {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    height: calc(100dvh - var(--topbar-h));
    min-height: 0;
    max-height: calc(100dvh - var(--topbar-h));
    padding: 24px 0;
  }

  .list-caption {
    margin-bottom: 8px;
    color: var(--faint);
    font-size: 11.5px;
  }

  .claim-rows {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    border-top: 1px solid var(--line);
  }

  .claim-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    width: 100%;
    padding: 10px 9px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: inherit;
    text-align: left;
    transition: background 0.12s var(--ease);
  }

  .claim-item:hover {
    background: var(--bg-hover);
  }

  .claim-item.active {
    background: var(--accent-soft);
  }

  .claim-text {
    display: block;
    overflow: hidden;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 500;
    line-height: 1.45;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .claim-item.active .claim-text {
    color: var(--ink);
    font-weight: 520;
  }

  .claim-sub {
    overflow: hidden;
    color: var(--faint);
    font-size: 11.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .claim-detail {
    min-width: 0;
    margin: 24px 0 40px;
  }

  .back-link {
    display: none;
    margin-bottom: 12px;
    color: var(--muted);
    font-size: 12px;
  }

  .kicker {
    margin-bottom: 5px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .statement {
    margin: 0 0 14px;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 27px;
    font-weight: 600;
    line-height: 1.32;
    letter-spacing: -0.015em;
    overflow-wrap: break-word;
  }

  .claim-meta {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 22px;
  }

  .claim-meta .spacer {
    flex: 1;
  }

  .claim-body {
    padding: 2px 0 10px;
  }

  .detail-section {
    margin-top: 30px;
    padding-top: 18px;
    border-top: 1px solid var(--line);
  }

  .detail-section .section-head {
    margin-bottom: 8px;
  }

  .detail-section h3 {
    margin: 0;
    color: var(--muted);
    font-size: 11px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .section-note {
    margin: 0 0 8px;
    color: var(--faint);
    font-size: 12px;
  }

  .provenance,
  .related {
    display: flex;
    flex-direction: column;
    min-width: 0;
    max-width: 100%;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    overflow: hidden;
  }

  .provenance-row,
  .related-row {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    padding: 8px 11px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: var(--bg-elevated);
    color: inherit;
    text-align: left;
  }

  .provenance-row:last-child,
  .related-row:last-child {
    border-bottom: 0;
  }

  .provenance-row:hover,
  .related-row:hover {
    background: var(--bg-hover);
  }

  .provenance-row :global(.ico),
  .related-row :global(.ico) {
    color: var(--muted);
  }

  .provenance-main,
  .related-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .provenance-title,
  .related-text {
    overflow: hidden;
    color: var(--ink-2);
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .provenance-sub,
  .related-sub {
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 1000px) {
    .claims-page.split {
      grid-template-columns: 300px minmax(0, 1fr);
      gap: 26px;
    }
  }

  @media (max-width: 860px) {
    .claims-page.split {
      display: block;
      height: auto;
      min-height: 0;
      max-width: var(--content-max);
      padding: 22px 20px 56px;
    }

    .claims-page.split .claims-list-pane {
      display: none;
    }

    .back-link {
      display: inline-block;
    }

    .claims-list-pane {
      position: static;
      max-height: none;
    }

    .claim-rows {
      overflow: visible;
    }

    .claim-detail {
      height: auto;
      margin: 0;
      overflow: visible;
      /* The detail frame keeps a right padding at every width: without it the body and the
         provenance/related cards run into the border. */
      padding: 20px 18px 26px;
    }

    .claim-body {
      min-width: 0;
    }
  }

  @media (max-width: 560px) {
    .claim-detail {
      padding: 18px 15px 22px;
    }
  }
</style>
