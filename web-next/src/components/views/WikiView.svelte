<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { untrack } from 'svelte';
  import { deleteWikiPage, saveWikiPage } from '../../lib/actions';
  import { api } from '../../lib/api';
  import { n, t } from '../../lib/i18n/index.svelte';
  import { navigate, router, sourcePath, wikiPath } from '../../lib/router.svelte';
  import AssistantBanner from '../assistant/AssistantBanner.svelte';
  import { rememberScroll } from '../../lib/scroll-memory';
  import { scrollFade } from '../../lib/scroll-fade';
  import { wiki } from '../../lib/stores/wiki.svelte';
  import EmptyState from '../ui/EmptyState.svelte';
  import Icon from '../ui/Icon.svelte';
  import Markdown from '../ui/Markdown.svelte';
  import CopyId from '../ui/CopyId.svelte';
  import ListHead from '../ui/ListHead.svelte';
  import VirtualList from '../ui/VirtualList.svelte';
  import type { WikiSummary } from '../../lib/api/types';

  const route = $derived(router.current);
  const isNew = $derived(route.view === 'wiki' && route.id === 'new');
  const detailOpen = $derived(route.view === 'wiki' && !!route.id);
  const selected = $derived(wiki.selected);

  let editing = $state(false);
  let preview = $state(false);
  let saving = $state(false);
  let title = $state('');
  let tags = $state('');
  let body = $state('');
  let query = $state('');
  let shownRoute = '';
  let listEl = $state<HTMLDivElement | undefined>();

  // An assistant result in the wiki is the record's set, in the record's order: a requested list
  // ("my pages about GitHub") is not the same thing as the whole wiki with a search box.
  const commandId = $derived(route.view === 'wiki' ? (route.params.get('command') ?? '') : '');
  // The record's items, in the record's order, WITH the model's reason for each: a researched set
  // is a judgement, and the reason is what lets the reader check it.
  let commandItems = $state<{ ref: string; why: string }[]>([]);

  $effect(() => {
    const id = commandId;
    if (!id) {
      commandItems = [];
      return;
    }
    untrack(() => {
      void api
        .command(id)
        .then((cmd) => {
          if (commandId === id) {
            commandItems = (cmd.items ?? []).map((item) => ({ ref: item.ref, why: item.why ?? '' }));
          }
        })
        .catch(() => {
          if (commandId === id) commandItems = [];
        });
    });
  });

  /** whyFor is the model's reason for this row, when the set was researched. */
  function whyFor(id: string): string {
    return commandItems.find((item) => item.ref === id)?.why ?? '';
  }

  const pages = $derived.by(() => {
    if (commandId) {
      const byId = new Map(wiki.pages.map((page) => [page.id, page]));
      return commandItems
        .map((item) => byId.get(item.ref))
        .filter((page): page is WikiSummary => page !== undefined);
    }
    const term = query.trim().toLowerCase();
    return wiki.filteredPages().filter((page) => !term || page.title.toLowerCase().includes(term));
  });

  $effect(() => {
    const current = router.current;
    if (current.view !== 'wiki') return;
    const key = current.key;
    if (key === shownRoute) return;
    shownRoute = key;

    if (current.id === 'new') {
      editing = true;
      preview = false;
      title = '';
      tags = '';
      body = '';
      return;
    }

    editing = false;
    preview = false;
    // Store methods read their own reactive fields; keep them out of this effect's dependencies.
    untrack(() => {
      if (current.id) void wiki.open(current.id);
      else wiki.clearSelection();
    });
  });

  $effect(() => {
    if (!wiki.loaded) untrack(() => void wiki.load());
  });

  let filterInitialized = false;
  $effect(() => {
    void wiki.filter;
    void query;
    // A local filter change starts its result list at the top; the first run must not fight the
    // history-entry restore after a back navigation.
    if (!filterInitialized) {
      filterInitialized = true;
      return;
    }
    if (listEl) listEl.scrollTop = 0;
  });

  function startNew(): void {
    navigate(wikiPath('new'));
  }

  function startEdit(): void {
    if (!selected) return;
    editing = true;
    preview = false;
    title = selected.title;
    tags = (selected.tags ?? []).join(', ');
    body = selected.body;
  }

  function cancel(): void {
    editing = false;
    preview = false;
    if (isNew) navigate('#/wiki');
  }

  async function save(): Promise<void> {
    if (saving) return;
    saving = true;
    try {
      const ok = await saveWikiPage(selected?.id ?? '', {
        title,
        tags: tags
          .split(',')
          .map((tag) => tag.trim())
          .filter(Boolean),
        body,
      });
      if (ok) {
        editing = false;
        preview = false;
      }
    } finally {
      saving = false;
    }
  }

  function chooseTag(tag: string): void {
    wiki.filter = wiki.filter === tag ? '' : tag;
    if (router.current.id) navigate('#/wiki');
  }

  function openSource(hash: string): void {
    navigate(sourcePath(hash));
  }
</script>

<div class="page wiki-page" class:split={detailOpen}>
  <section class="wiki-list-pane" aria-label={t('wiki.listAria')}>
    <ListHead title={t('view.wiki')} count={wiki.loaded ? wiki.count : null}>
      {#snippet action()}
        <button class="btn btn--primary" onclick={startNew}>
          <Icon name="plus" size={14} /> <span class="btn-text">{t('wiki.newPage')}</span>
        </button>
      {/snippet}

      {#if commandId}
        <AssistantBanner
          id={commandId}
          total={commandItems.length}
          lens="wiki"
        />
      {/if}

      <div class="search-wrap list-search">
        <Icon name="search" size={14} />
        <input
          use:clearable
          class="input input--search"
          placeholder={t('wiki.filterPlaceholder')}
          bind:value={query}
          oninput={() => {
            // Typing takes the filter back from the assistant.
            if (commandId) navigate('#/wiki');
          }}
          aria-label={t('wiki.filterAria')}
        />
      </div>

      {#if wiki.tags.length > 0 && !commandId}
        <div
          class="chip-cloud"
          use:scrollFade
          use:rememberScroll={'wiki-tags'}
          aria-label={t('wiki.tagsAria')}
        >
          {#each wiki.tags as tag (tag.tag)}
            <button
              class="chip"
              class:chip--accent={wiki.filter === tag.tag}
              onclick={() => chooseTag(tag.tag)}
            >
              {tag.tag}<span class="chip-count">{n(tag.count)}</span>
            </button>
          {/each}
        </div>
      {/if}
      {#if wiki.filter}
        <div class="filter-line">
          {t('wiki.filteredBy')} <b>{wiki.filter}</b>
          <button class="link-button" onclick={() => (wiki.filter = '')}>{t('common.clear')}</button>
        </div>
      {/if}
    </ListHead>

    <div class="page-rows" bind:this={listEl} use:scrollFade use:rememberScroll={'wiki-list'}>
      {#if wiki.error}
        <p class="notice notice--error">{wiki.error}</p>
      {:else if !wiki.loaded}
        <div class="stack">
          {#each Array(4) as _, index (index)}<span class="skeleton" style="height:42px"></span>{/each}
        </div>
      {:else if pages.length === 0}
        <EmptyState
          icon="wiki"
          title={wiki.filter || query ? t('wiki.noMatch') : t('wiki.emptyTitle')}
          text={wiki.filter || query ? t('wiki.noMatchText') : t('wiki.emptyText')}
        >
          <button class="btn btn--primary btn--sm" onclick={startNew}>
            <Icon name="plus" size={13} /> {t('wiki.writeFirst')}
          </button>
        </EmptyState>
      {:else}
        <VirtualList items={pages} rowHeight={88} key={(page) => page.id}>
          {#snippet row(page: WikiSummary)}
            <button
              class="page-item"
              class:active={selected?.id === page.id}
              title={whyFor(page.id)}
              onclick={() => navigate(wikiPath(page.id))}
            >
              <span class="page-title">{page.title || t('wiki.untitled')}</span>
              {#if (page.tags?.length ?? 0) + (page.auto_tags?.length ?? 0) > 0}
                <span class="page-tags">
                  {#each page.tags ?? [] as tag (tag)}<span class="tag">{tag}</span>{/each}
                  {#each page.auto_tags ?? [] as tag (tag)}<span class="tag tag--auto">{tag}</span>{/each}
                </span>
              {/if}
              <span class="page-date">{page.updated_at?.slice(0, 10) ?? ''}</span>
            </button>
          {/snippet}
        </VirtualList>
      {/if}
    </div>
  </section>

  {#if detailOpen}
    <section class="wiki-detail detail-frame" aria-label={t('wiki.detailAria')}>
      <a class="back-link" href="#/wiki">{t('wiki.backAll')}</a>

      {#if editing}
        <form
          class="editor"
          onsubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <div class="editor-head">
            <div>
              <div class="kicker">{isNew ? t('wiki.newPage') : t('wiki.editPage')}</div>
              <h2>{title.trim() || t('wiki.untitled')}</h2>
            </div>
            <div class="seg" role="tablist" aria-label={t('wiki.editorMode')}>
              <button type="button" class:on={!preview} onclick={() => (preview = false)}>{t('wiki.write')}</button>
              <button type="button" class:on={preview} onclick={() => (preview = true)}>{t('wiki.preview')}</button>
            </div>
          </div>

          {#if preview}
            <div class="preview">
              {#if tags.trim()}
                <div class="page-tags preview-tags">
                  {#each tags.split(',').map((tag) => tag.trim()).filter(Boolean) as tag (tag)}
                    <span class="chip chip--accent">{tag}</span>
                  {/each}
                </div>
              {/if}
              {#if body.trim()}
                <Markdown text={body} />
              {:else}
                <p class="muted">{t('wiki.nothingToPreview')}</p>
              {/if}
            </div>
          {:else}
            <label class="field">
              <span>{t('wiki.fieldTitle')}</span>
              <input
                use:clearable
                class="input"
                bind:value={title}
                placeholder={t('wiki.titlePlaceholder')}
                autocomplete="off"
              />
            </label>
            <label class="field">
              <span>{t('wiki.fieldTags')} <span class="hint">{t('wiki.commaSeparated')}</span></span>
              <input use:clearable class="input" bind:value={tags} placeholder="go, concurrency, notes" autocomplete="off" />
            </label>
            <label class="field">
              <span>Markdown</span>
              <textarea
                use:clearable
                class="textarea editor-body"
                bind:value={body}
                spellcheck="true"
                placeholder={t('wiki.bodyPlaceholder')}
              ></textarea>
            </label>
          {/if}

          <div class="editor-foot">
            <button
              class="btn btn--primary btn--action"
              type="submit"
              disabled={saving}
              title={saving ? t('wiki.saving') : t('wiki.savePage')}
            >
              {#if saving}<span class="spinner"></span>{:else}<Icon name="check" size={14} />{/if}
              <span class="btn-label">{saving ? t('wiki.saving') : t('wiki.savePage')}</span>
            </button>
            <button
              class="btn btn--ghost btn--action"
              type="button"
              title={t('common.cancel')}
              onclick={cancel}
            >
              <Icon name="close" size={14} /> <span class="btn-label">{t('common.cancel')}</span>
            </button>
            <span class="muted small">{t('wiki.saveHint')}</span>
          </div>
        </form>
      {:else if wiki.detailError}
        <p class="notice notice--error">{wiki.detailError}</p>
      {:else if wiki.detailLoading && !selected}
        <div class="stack">
          <span class="skeleton" style="height:28px;width:55%"></span>
          <span class="skeleton" style="height:18px;width:35%"></span>
          <span class="skeleton" style="height:180px"></span>
        </div>
      {:else if selected}
        <article class="page-detail">
          <header class="page-detail-head">
            <div class="page-detail-title">
              <div class="kicker">{t('wiki.kicker')}</div>
              <h2>{selected.title || t('wiki.untitled')}</h2>
              <div class="page-id">
                <span class="muted small">ID</span>
                <CopyId text={selected.id} label={t('wiki.copyLabel')} display={selected.id.slice(0, 10)} />
                {#if selected.updated_at}
                  <span class="muted small">{t('wiki.updatedAt', { date: selected.updated_at.slice(0, 10) })}</span>
                {/if}
              </div>
            </div>
            <div class="page-actions">
              {#if selected.source_hash}
                <button
                  class="btn btn--secondary btn--sm btn--action"
                  title={t('wiki.openSource')}
                  onclick={() => openSource(selected!.source_hash!)}
                >
                  <Icon name="sources" size={13} /> <span class="btn-label">{t('wiki.openSource')}</span>
                </button>
              {/if}
              <button
                class="btn btn--secondary btn--sm btn--action"
                title={t('wiki.edit')}
                onclick={startEdit}
              >
                <Icon name="edit" size={13} /> <span class="btn-label">{t('wiki.edit')}</span>
              </button>
              <button
                class="btn btn--danger btn--sm btn--action"
                title={t('common.delete')}
                onclick={() => deleteWikiPage(selected!.id)}
              >
                <Icon name="trash" size={13} /> <span class="btn-label">{t('common.delete')}</span>
              </button>
            </div>
          </header>

          {#if (selected.tags?.length ?? 0) + (selected.auto_tags?.length ?? 0) > 0}
            <div class="page-tags page-tags--detail">
              {#each selected.tags ?? [] as tag (tag)}
                <button class="chip chip--accent" onclick={() => chooseTag(tag)}>{tag}</button>
              {/each}
              {#each selected.auto_tags ?? [] as tag (tag)}
                <button
                  class="chip"
                  title={t('wiki.autoTagTitle')}
                  onclick={() => chooseTag(tag)}>{tag}<span class="auto-mark">{t('wiki.autoTag')}</span></button
                >
              {/each}
            </div>
          {/if}

          <div class="page-markdown">
            <Markdown text={selected.body} />
          </div>
        </article>
      {/if}
    </section>
  {/if}
</div>

<style>
  .wiki-page {
    padding-top: 24px;
  }

  .wiki-page.split {
    display: grid;
    grid-template-columns: minmax(280px, 350px) minmax(0, 1fr);
    gap: 38px;
    align-items: start;
    height: auto;
    min-height: calc(100dvh - var(--topbar-h));
    max-width: 1280px;
    padding-top: 0;
    padding-bottom: 0;
  }

  .wiki-list-pane {
    min-width: 0;
  }

  .split .wiki-list-pane {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    height: calc(100dvh - var(--topbar-h));
    min-height: 0;
    max-height: calc(100dvh - var(--topbar-h));
    padding: 24px 0;
  }

  .page-rows {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    border-top: 1px solid var(--line);
  }

  .page-item {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: 3px 8px;
    width: 100%;
    padding: 10px 9px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: inherit;
    text-align: left;
    transition: background 0.12s var(--ease);
  }

  .page-item:hover {
    background: var(--bg-hover);
  }

  .page-item.active {
    background: var(--accent-soft);
  }

  .page-title {
    grid-column: 1;
    overflow: hidden;
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 500;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .page-date {
    grid-column: 2;
    grid-row: 1;
    color: var(--faint);
    font-size: 10.5px;
  }

  .page-tags {
    grid-column: 1 / -1;
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 2px;
  }

  .tag--auto,
  .auto-mark {
    opacity: 0.62;
  }

  .auto-mark {
    margin-left: 4px;
    font-size: 8.5px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .wiki-detail {
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
    margin-bottom: 4px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.1em;
    text-transform: uppercase;
  }

  .page-detail-head {
    display: flex;
    align-items: flex-start;
    gap: 16px;
    margin-bottom: 18px;
  }

  .page-detail-title {
    min-width: 0;
    flex: 1;
  }

  .page-detail-title h2 {
    margin: 0 0 7px;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 31px;
    font-weight: 600;
    line-height: 1.2;
    letter-spacing: -0.02em;
  }

  .page-id {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }

  .page-actions {
    display: flex;
    align-items: center;
    gap: 5px;
    flex: none;
  }

  .page-tags--detail {
    margin: 0 0 20px;
  }

  .page-markdown {
    padding-top: 2px;
  }

  .editor-head {
    display: flex;
    align-items: flex-start;
    gap: 14px;
    margin-bottom: 18px;
  }

  .editor-head > div:first-child {
    flex: 1;
    min-width: 0;
  }

  .editor-head h2 {
    margin: 0;
    color: var(--ink);
    font-family: var(--font-sans);
    font-size: 28px;
    font-weight: 600;
    line-height: 1.25;
  }

  .editor-body {
    min-height: 360px;
    font-family: var(--font-mono);
    font-size: 12.5px;
    line-height: 1.65;
  }

  .editor-foot {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 14px;
  }

  .preview {
    min-height: 220px;
    padding: 18px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
  }

  .preview-tags {
    margin-bottom: 16px;
  }

  @media (max-width: 1000px) {
    .wiki-page.split {
      grid-template-columns: 300px minmax(0, 1fr);
      gap: 26px;
    }
  }

  @media (max-width: 860px) {
    .wiki-page.split {
      display: block;
      height: auto;
      min-height: 0;
      max-width: var(--content-max);
      padding: 22px 20px 56px;
    }

    .wiki-page.split .wiki-list-pane {
      display: none;
    }

    .back-link {
      display: inline-block;
    }

    .wiki-list-pane {
      position: static;
      max-height: none;
    }

    .page-rows {
      overflow: visible;
    }

    .wiki-detail {
      height: auto;
      margin: 0;
      overflow: visible;
      /* Keep the frame's right padding: a zeroed one let the page title, tags and body run into the
         border on a narrow screen. */
      padding: 20px 18px 26px;
    }

    .page-detail-head {
      flex-direction: column;
    }
  }

  @media (max-width: 560px) {
    .wiki-detail {
      padding: 18px 15px 22px;
    }
  }
</style>
