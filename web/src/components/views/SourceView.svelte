<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import { clearable } from '../../lib/clearable';
  import {
    attachSnapshot,
    captureSource,
    deleteSource,
    distillSource,
    rebuildSourceTree,
    refetchSource,
    refreshSource,
    resetSource,
  } from '../../lib/actions';
  import { api } from '../../lib/api';
  import { fmtBytes, hostOf, prettyJson, relTime, splitFrontmatter } from '../../lib/format';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { askSourcePath, navigate, previousView, router, sourcePath, wikiPath } from '../../lib/router.svelte';
  import { setPagePosition } from '../../lib/scroll-memory';
  import { sourcesTop } from '../../lib/sources-scroll';
  import { sources } from '../../lib/stores/sources.svelte';
  import { countsOf, canAttachSnapshot, progressOf, statusLabel } from '../../lib/source-state';
  import type { SourceNode, SourceTopic } from '../../lib/api/types';
  import CopyId from '../ui/CopyId.svelte';
  import Icon from '../ui/Icon.svelte';
  import Frontmatter from '../ui/Frontmatter.svelte';
  import Markdown from '../ui/Markdown.svelte';
  import SourceIcon from '../sources/SourceIcon.svelte';
  import SourceSteps from '../sources/SourceSteps.svelte';

  const RENDER_CAP = 64 * 1024;
  const FILE_CAP = 128 * 1024;
  const LOG_CAP = 96 * 1024;

  const route = $derived(router.current);
  const hash = $derived(route.view === 'source' ? route.id : '');
  const tab = $derived(route.params.get('tab') || 'snapshot');
  const partID = $derived(route.params.get('part') ?? '');
  const fileParam = $derived(route.params.get('file') ?? '');
  const selected = $derived(sources.selected);
  const sidecars = $derived(selected?.sidecars ?? []);

  const isPDF = $derived(selected?.ext === '.pdf');
  const isURLSource = $derived(selected?.kind === 'url');
  const markdownView = $derived(
    sources.contentIsRendering || selected?.ext === '.md' || selected?.ext === '.markdown' || selected?.kind === 'url',
  );

  const doc = $derived(splitFrontmatter(sources.content ?? ''));
  const preview = $derived((sources.content ?? '').slice(0, RENDER_CAP));
  const truncated = $derived((sources.content?.length ?? 0) > RENDER_CAP);
  const big = $derived((sources.content?.length ?? 0) > RENDER_CAP);
  const parts = $derived(sources.tree.filter((node) => node.depth > 0));
  const readable = $derived(parts.filter((node) => node.has_text));
  const partNode = $derived(sources.tree.find((node) => node.node_id === partID));
  const openFile = $derived(
    fileParam
      ? sidecars.find((sidecar) => sidecar.name === fileParam) ?? { name: fileParam, bytes: -1, object: false }
      : null,
  );
  const hasChildrenTab = $derived(
    !!selected && (selected.sitemap === true || selected.rss === true || (selected.children ?? 0) > 0),
  );
  const live = $derived(sources.progress[hash]);
  const counts = $derived(selected ? countsOf(selected, live) : null);
  const progress = $derived(selected ? progressOf(selected, live) : null);

  const topics = $derived.by((): SourceTopic[] => {
    if (sources.atomTopics.length > 0) return sources.atomTopics;
    const counts = new Map<string, number>();
    for (const atom of sources.atoms) {
      if (atom.topic) counts.set(atom.topic, (counts.get(atom.topic) ?? 0) + 1);
    }
    return [...counts.entries()]
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
  });

  let partText = $state<string | null>(null);
  let partError = $state('');
  let partSeq = 0;
  let loadedPartKey = '';

  let fileText = $state<string | null>(null);
  let fileError = $state('');
  let fileSeq = 0;
  let loadedFileKey = '';

  let pdfReady = $state(false);

  // A URL the app could not read (a login, a paywall, a JavaScript page that became a bookmark): the
  // way out is the human's browser plus a file they attach here. An attached snapshot can be
  // REPLACED, and the URL can be tried again — which only replaces it when the page is readable.
  const attachable = $derived(!!selected && canAttachSnapshot(selected));
  const attached = $derived(!!selected?.attached);
  const attachedFrom = $derived(selected?.attached_from ?? '');
  let attachInput: HTMLInputElement | undefined = $state();
  let attaching = $state(false);
  let refetching = $state(false);

  async function onAttachFile(files: FileList | null): Promise<void> {
    if (!selected || !files || files.length === 0 || attaching) return;
    attaching = true;
    try {
      await attachSnapshot(selected.hash, files[0]);
    } finally {
      attaching = false;
      if (attachInput) attachInput.value = '';
    }
  }

  async function onRefetch(): Promise<void> {
    if (!selected || refetching) return;
    refetching = true;
    try {
      await refetchSource(selected.hash);
    } finally {
      refetching = false;
    }
  }

  // The rail's tag editor: the source's human labels. The .meta record is the truth; the API
  // normalizes the text and mirrors it into the index, and the source event refreshes the row.
  let tagsEditing = $state(false);
  let tagsDraft = $state('');
  let tagsSaving = $state(false);
  let tagsError = $state('');

  const logTail = $derived(sources.log.length > LOG_CAP ? sources.log.slice(-LOG_CAP) : sources.log);

  $effect(() => {
    const current = router.current;
    if (current.view !== 'source' || !current.id) return;
    // sources.open reads selectedHash; calling it tracked would reload the same source in a loop.
    untrack(() => void sources.open(current.id));
  });

  onDestroy(() => {
    sources.clearSelection();
  });

  $effect(() => {
    const id = partID;
    const currentHash = hash;
    void sources.version;
    const seq = ++partSeq;
    if (!currentHash || !id) {
      partText = null;
      partError = '';
      loadedPartKey = '';
      return;
    }
    const key = `${currentHash}|${id}`;
    if (key !== loadedPartKey) {
      partText = null;
      partError = '';
    }
    loadedPartKey = key;
    void sources
      .nodeText(currentHash, id)
      .then((text) => {
        if (seq !== partSeq || hash !== currentHash || partID !== id) return;
        partText = text;
        partError = '';
      })
      .catch((error) => {
        if (seq !== partSeq || hash !== currentHash || partID !== id) return;
        partText = '';
        partError = error instanceof Error ? error.message : String(error);
      });
  });

  $effect(() => {
    const name = fileParam;
    const currentHash = hash;
    const currentTab = tab;
    const seq = ++fileSeq;
    if (!currentHash || !name || currentTab !== 'files') {
      fileText = null;
      fileError = '';
      loadedFileKey = '';
      return;
    }
    const key = `${currentHash}|${name}`;
    if (key !== loadedFileKey) {
      fileText = null;
      fileError = '';
    }
    loadedFileKey = key;
    const sidecar = sidecars.find((entry) => entry.name === name);
    if (!sidecar) {
      fileText = null;
      fileError = t('source.fileNotListed');
      return;
    }
    if (sidecar.object && isPDF) {
      fileText = null;
      fileError = '';
      return;
    }
    void api
      .rawText(currentHash, sidecar.object ? 'object' : sidecar.name)
      .then((text) => {
        if (seq === fileSeq && hash === currentHash && fileParam === name) fileText = prettyJson(text);
      })
      .catch((error) => {
        if (seq === fileSeq && hash === currentHash && fileParam === name) {
          fileError = error instanceof Error ? error.message : String(error);
        }
      });
  });

  // The Summary and Sub-sources tabs read their data when they open (and again when a source event
  // says something changed) — never on every source detail load.
  $effect(() => {
    const currentHash = hash;
    const currentTab = tab;
    void sources.version;
    if (!currentHash || currentTab !== 'summary') return;
    void sources.loadSummary(currentHash, true);
  });

  $effect(() => {
    const currentHash = hash;
    const currentTab = tab;
    void sources.version;
    if (!currentHash || currentTab !== 'children') return;
    void sources.loadChildren(currentHash, true);
  });

  $effect(() => {
    // A different source starts a closed, empty tag editor.
    void hash;
    tagsEditing = false;
    tagsError = '';
  });

  $effect(() => {
    if (tab !== 'pdf') {
      pdfReady = false;
      return;
    }
    const frame = requestAnimationFrame(() => (pdfReady = true));
    return () => cancelAnimationFrame(frame);
  });

  function go(next: { tab?: string; part?: string; file?: string }): void {
    if (!hash) return;
    const params = new URLSearchParams();
    const nextTab = next.tab ?? tab;
    if (nextTab && nextTab !== 'snapshot') params.set('tab', nextTab);
    if (next.part) params.set('part', next.part);
    if (next.file) params.set('file', next.file);
    const query = params.toString();
    navigate(`#/sources/${encodeURIComponent(hash)}${query ? `?${query}` : ''}`);
  }

  function setTab(next: string): void {
    go({ tab: next });
  }

  function openPart(node: SourceNode): void {
    go({ tab: 'snapshot', part: node.node_id });
  }

  function viewFile(name: string): void {
    go({ tab: 'files', file: name });
  }

  function beginTags(): void {
    tagsDraft = (selected?.tags ?? []).join(', ');
    tagsEditing = true;
    tagsError = '';
  }

  async function saveTags(): Promise<void> {
    if (!hash) return;
    const tags = tagsDraft
      .split(',')
      .map((tag) => tag.trim())
      .filter((tag) => tag !== '');
    tagsSaving = true;
    tagsError = '';
    try {
      await sources.setTags(hash, tags);
      tagsEditing = false;
    } catch (error) {
      tagsError = error instanceof Error ? error.message : String(error);
    } finally {
      tagsSaving = false;
    }
  }

  function backToSources(): void {
    // Entered from the list: a REAL back returns to that very history entry, with the list's own
    // scroll state. Anything else (a claim's provenance, a search result, a cold start) targets the
    // list, so the last list offset is seeded into the fresh entry instead of opening at the top.
    if (previousView() === 'sources') {
      history.back();
      return;
    }
    const top = sourcesTop();
    navigate('#/sources');
    setPagePosition(top);
  }
</script>

{#if route.view === 'source' && hash}
  <div class="page page--wide source-page">
    {#if sources.detailError}
      <p class="notice notice--error">{sources.detailError}</p>
      <button
        class="btn btn--primary btn--action"
        title={t('common.retry')}
        onclick={() => void sources.refresh(hash)}
      >
        <Icon name="refresh" size={14} /> <span class="btn-label">{t('common.retry')}</span>
      </button>
      <button
        class="btn btn--secondary btn--action"
        title={t('source.backToSources')}
        onclick={backToSources}
      >
        <Icon name="arrowLeft" size={14} /> <span class="btn-label">{t('source.backToSources')}</span>
      </button>
    {:else if selected}
      <header class="source-head">
        <button
          class="icon-btn"
          title={t('source.backToSources')}
          aria-label={t('source.backToSources')}
          onclick={backToSources}
        >
          <Icon name="arrowLeft" size={17} />
        </button>
        <span class="source-head-icon"><SourceIcon kind={selected.kind} hash={selected.hash} size={19} /></span>
        <div class="source-head-main">
          <h1>{selected.title || selected.hash.slice(0, 12)}</h1>
          <div class="source-head-sub">
            <span class="chip chip--mono">{selected.kind}</span>
            {#if selected.sitemap}<span class="chip">{t('source.sitemap')}</span>{/if}
            {#if selected.rss}<span class="chip">{t('source.feed')}</span>{/if}
            <span>{fmtBytes(selected.bytes)}</span>
            <span>·</span>
            <span>{relTime(selected.imported_at)}</span>
            {#if selected.language}<span>·</span><span>{selected.language}</span>{/if}
            {#if selected.pages}<span>·</span><span>{tn('source.pages', selected.pages)}</span>{/if}
          </div>
        </div>
        <div class="source-head-actions">
          <button
            class="btn btn--primary btn--sm btn--action"
            title={t('source.ask')}
            onclick={() => navigate(askSourcePath(selected!.hash))}
          >
            <Icon name="ask" size={13} /> <span class="btn-label">{t('source.ask')}</span>
          </button>
          {#if isURLSource && selected.url}
            <a
              class="btn btn--secondary btn--sm btn--action"
              href={selected.url}
              target="_blank"
              rel="noopener noreferrer"
              title={t('source.openUrl')}
            >
              <Icon name="external" size={13} /> <span class="btn-label">{t('source.openUrl')}</span>
            </a>
          {/if}
          {#if selected.rss || selected.sitemap}
            <button
              class="btn btn--secondary btn--sm btn--action"
              title={t('source.refresh')}
              onclick={() => void refreshSource(selected!.hash)}
            >
              <Icon name="refresh" size={13} /> <span class="btn-label">{t('source.refresh')}</span>
            </button>
          {:else}
            <button
              class="btn btn--secondary btn--sm btn--action"
              title={t('source.capture')}
              onclick={() => void captureSource(selected!.hash)}
            >
              <Icon name="capture" size={13} /> <span class="btn-label">{t('source.capture')}</span>
            </button>
            <button
              class="btn btn--secondary btn--sm btn--action"
              title={t('source.outline')}
              onclick={() => void rebuildSourceTree(selected!.hash)}
            >
              <Icon name="tree" size={13} /> <span class="btn-label">{t('source.outline')}</span>
            </button>
            <button
              class="btn btn--primary btn--sm btn--action"
              title={t('source.distill')}
              onclick={() => void distillSource(selected!.hash)}
            >
              <Icon name="distill" size={13} /> <span class="btn-label">{t('source.distill')}</span>
            </button>
            <button
              class="btn btn--secondary btn--sm btn--action"
              title={t('source.reset')}
              onclick={() => void resetSource(selected!.hash)}
            >
              <Icon name="reset" size={13} /> <span class="btn-label">{t('source.reset')}</span>
            </button>
          {/if}
          {#if !selected.origin && !selected.original_name?.startsWith('wiki:')}
            <button
              class="btn btn--danger btn--sm btn--action"
              title={t('common.delete')}
              onclick={() => void deleteSource(selected!.hash)}
            >
              <Icon name="trash" size={13} /> <span class="btn-label">{t('common.delete')}</span>
            </button>
          {/if}
        </div>
      </header>

      <div class="source-layout">
        <section class="source-content">
          <div class="tabs" role="tablist" aria-label={t('source.viewAria')}>
            {#if isURLSource}
              <button class="tab" class:on={tab === 'snapshot'} onclick={() => setTab('snapshot')}>{t('source.tabSnapshot')}</button>
              <button class="tab" class:on={tab === 'live'} onclick={() => setTab('live')}>{t('source.tabLive')}</button>
            {:else}
              <button class="tab" class:on={tab === 'snapshot'} onclick={() => setTab('snapshot')}>
                {isPDF ? t('source.tabText') : t('source.tabContent')}
              </button>
              {#if isPDF}
                <button class="tab" class:on={tab === 'pdf'} onclick={() => setTab('pdf')}>PDF</button>
              {/if}
            {/if}
            {#if !selected.bookmark && selected.steps?.distill?.state !== 'skipped'}
              <button class="tab" class:on={tab === 'summary'} onclick={() => setTab('summary')}>{t('source.tabSummary')}</button>
            {/if}
            <button class="tab" class:on={tab === 'log'} onclick={() => setTab('log')}>{t('source.tabLog')}</button>
            <button class="tab" class:on={tab === 'files'} onclick={() => setTab('files')}>{t('source.tabFiles')}</button>
            {#if hasChildrenTab}
              <button class="tab" class:on={tab === 'children'} onclick={() => setTab('children')}>
                {t('source.tabChildren')}{selected.children ? ` ${n(selected.children)}` : ''}
              </button>
            {/if}
          </div>

          <div class="source-body">
            {#if tab === 'summary'}
              {#if sources.summary && !sources.summaryMissing}
                <div class="summary-head">
                  <p class="file-intro muted">{t('source.summaryIntro')}</p>
                  <button
                    class="btn btn--ghost btn--sm btn--action"
                    disabled={sources.summaryGenerating}
                    title={t('source.rewriteTitle')}
                    onclick={() => void sources.generateSummary(selected!.hash)}
                  >
                    <Icon name="refresh" size={13} />
                    <span class="btn-label"
                      >{sources.summaryGenerating ? t('source.rewriting') : t('source.rewrite')}</span
                    >
                  </button>
                </div>
                {#if sources.summaryError}
                  <p class="notice notice--error">{sources.summaryError}</p>
                {/if}
                <Markdown text={sources.summary} wikilinks={false} />
              {:else if sources.summaryLoading}
                <p class="file-notice muted">{t('source.loadingSummary')}</p>
              {:else}
                <div class="summary-empty">
                  <p class="muted">{t('source.summaryEmpty')}</p>
                  {#if selected.steps?.distill?.state === 'ok'}
                    <button
                      class="btn btn--primary btn--sm btn--action"
                      disabled={sources.summaryGenerating}
                      title={t('source.generateSummary')}
                      onclick={() => void sources.generateSummary(selected!.hash)}
                    >
                      <Icon name="spark" size={13} />
                      <span class="btn-label"
                        >{sources.summaryGenerating
                          ? t('source.generating')
                          : t('source.generateSummary')}</span
                      >
                    </button>
                  {:else}
                    <p class="section-note">{t('source.summaryNeedsDistill')}</p>
                  {/if}
                  {#if sources.summaryError}
                    <p class="notice notice--error">{sources.summaryError}</p>
                  {/if}
                </div>
              {/if}
            {:else if tab === 'children'}
              {#if sources.childrenError}
                <p class="notice notice--error file-notice">{sources.childrenError}</p>
              {:else if !sources.childrenLoaded}
                <p class="file-notice muted">{t('common.loading')}</p>
              {:else if sources.children.length === 0}
                <p class="file-notice muted">{t('source.childrenEmpty')}</p>
              {:else}
                <p class="file-intro muted">{t('source.childrenIntro')}</p>
                <div class="file-list">
                  {#each sources.children as child (child.hash)}
                    <div class="file-row">
                      <SourceIcon kind={child.kind} hash={child.hash} size={14} />
                      <button class="file-open" onclick={() => navigate(sourcePath(child.hash))}>
                        {child.title || child.hash.slice(0, 12)}
                      </button>
                      <span class="chip">{child.kind}</span>
                      <span class="grow"></span>
                      <span class="muted">{relTime(child.imported_at)}</span>
                    </div>
                  {/each}
                </div>
              {/if}
            {:else if tab === 'files'}
              {#if openFile}
                <div class="file-head">
                  <button class="icon-btn" title={t('source.backToFiles')} onclick={() => go({ tab: 'files' })}>
                    <Icon name="chevronLeft" size={16} />
                  </button>
                  <code>{openFile.name}</code>
                  {#if openFile.object}<span class="chip">object</span>{/if}
                  <span class="spacer"></span>
                  {#if isPDF && openFile.object}
                    <button
                      class="btn btn--secondary btn--sm btn--action"
                      title={t('source.openPdf')}
                      onclick={() => setTab('pdf')}
                    >
                      <Icon name="filePdf" size={13} /> <span class="btn-label">{t('source.openPdf')}</span>
                    </button>
                  {/if}
                </div>
                {#if fileError}
                  <p class="notice notice--error file-notice">{fileError}</p>
                {:else if isPDF && openFile.object}
                  <p class="file-notice muted">{t('source.pdfNotText')}</p>
                {:else if fileText === null}
                  <p class="file-notice muted">{t('common.loading')}</p>
                {:else}
                  {#if fileText.length > FILE_CAP}
                    <p class="file-notice muted">
                      {t('source.fileShowingFirst', {
                        size: fmtBytes(FILE_CAP),
                        total: fmtBytes(fileText.length),
                      })}
                    </p>
                  {/if}
                  <pre class="raw-file">{fileText.slice(0, FILE_CAP)}</pre>
                {/if}
              {:else}
                <p class="file-intro muted">
                  {t('source.filesIntroStart')}
                  <b>—</b>
                  {t('source.filesIntroEnd')}
                </p>
                <div class="file-list">
                  {#each sidecars as sidecar (sidecar.name)}
                    <div class="file-row" class:missing={sidecar.bytes < 0}>
                      {#if sidecar.bytes < 0}
                        <Icon name="file" size={14} />
                        <code>{sidecar.name}</code>
                        <span class="grow"></span>
                        <span class="muted">{t('source.missing')}</span>
                      {:else}
                        <button class="file-open" onclick={() => viewFile(sidecar.name)}>
                          <Icon name="file" size={14} />
                          <code>{sidecar.name}</code>
                        </button>
                        {#if sidecar.object}<span class="chip">object</span>{/if}
                        <span class="grow"></span>
                        <span class="muted">{fmtBytes(sidecar.bytes)}</span>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
            {:else if tab === 'log'}
              {#if sources.log.length > LOG_CAP}
                <p class="file-notice muted">
                  {t('source.logShowingLast', {
                    size: fmtBytes(LOG_CAP),
                    total: fmtBytes(sources.log.length),
                  })}
                </p>
              {/if}
              <pre class="raw-file log-file">{logTail || sources.logError || t('source.noLog')}</pre>
            {:else if tab === 'live' && selected.url}
              <iframe class="frame" title={t('source.liveTitle')} src={selected.url}></iframe>
            {:else if tab === 'pdf' && isPDF}
              {#if pdfReady}
                <iframe class="frame" title={selected.title || 'PDF'} src={api.rawURL(selected.hash)}></iframe>
              {:else}
                <p class="file-notice muted">{t('source.loadingPdf')}</p>
              {/if}
            {:else if partID}
              <div class="part-head">
                <button class="icon-btn" title={t('source.backToOutline')} onclick={() => go({ tab: 'snapshot' })}>
                  <Icon name="chevronLeft" size={16} />
                </button>
                <code>{partID}</code>
                <span>{partNode?.title || ''}</span>
              </div>
              {#if partError}
                <p class="file-notice muted">
                  {t('source.partGone', { error: partError })}
                  <button
                    class="btn btn--secondary btn--sm btn--action"
                    title={t('source.showWhole')}
                    onclick={() => go({ tab: 'snapshot' })}
                  >
                    <Icon name="eye" size={13} /> <span class="btn-label">{t('source.showWhole')}</span>
                  </button>
                </p>
              {:else if partText === null}
                <p class="file-notice muted">{t('common.loading')}</p>
              {:else}
                {@const partDoc = splitFrontmatter(partText.slice(0, RENDER_CAP))}
                {#if partText.length > RENDER_CAP}
                  <p class="file-notice muted">
                    {t('source.partTooBig', {
                      size: fmtBytes(partText.length),
                      shown: fmtBytes(RENDER_CAP),
                    })}
                  </p>
                {/if}
                {#if partDoc.front}<Frontmatter text={partDoc.front} />{/if}
                {#if markdownView}
                  <Markdown text={partDoc.body} wikilinks={false} />
                {:else}
                  <pre class="raw-file">{partDoc.body}</pre>
                {/if}
              {/if}
            {:else if sources.content === null}
              {#if sources.contentError}
                <p class="notice notice--error file-notice">{sources.contentError}</p>
              {:else}
                <p class="file-notice muted">{t('common.loading')}</p>
              {/if}
            {:else if big && readable.length > 0}
              <div class="outline-intro">
                <p class="muted">
                  {t('source.tooBig', { size: fmtBytes(sources.content?.length ?? 0) })}
                  {tn('source.readableParts', readable.length)}
                </p>
              </div>
              <div class="outline">
                {#each parts as node (node.node_id)}
                  {#if node.has_text}
                    <button class="outline-row" style="padding-left:{10 + Math.max(0, node.depth - 1) * 14}px" onclick={() => openPart(node)}>
                      <span class="outline-id">{node.node_id}</span>
                      <span class="outline-title">{node.title || t('source.untitled')}</span>
                      {#if node.ocr}<span class="chip">ocr</span>{/if}
                    </button>
                  {:else}
                    <div class="outline-row outline-row--group" style="padding-left:{10 + Math.max(0, node.depth - 1) * 14}px">
                      <span class="outline-id">{node.node_id}</span>
                      <span class="outline-title">{node.title || t('source.untitled')}</span>
                    </div>
                  {/if}
                {/each}
              </div>
            {:else if sources.content === '' && isPDF}
              <p class="file-notice muted">
                {t('source.noTextYet')}
                <button
                  class="btn btn--secondary btn--sm btn--action"
                  title={t('source.openPdf')}
                  onclick={() => setTab('pdf')}
                >
                  <Icon name="filePdf" size={13} /> <span class="btn-label">{t('source.openPdf')}</span>
                </button>
              </p>
            {:else}
              {#if truncated}
                <p class="file-notice muted">
                  {t('source.noOutlineYet', {
                    size: fmtBytes(RENDER_CAP),
                    total: fmtBytes(sources.content?.length ?? 0),
                  })}
                </p>
              {/if}
              {#if doc.front}<Frontmatter text={doc.front} />{/if}
              {#if markdownView}
                <Markdown text={doc.body} wikilinks={false} />
              {:else}
                <pre class="raw-file">{preview}</pre>
              {/if}
            {/if}
          </div>
        </section>

        <aside class="source-rail" aria-label={t('source.detailsAria')}>
          <section class="rail-block">
            <div class="rail-label">{t('source.steps')}</div>
            {#if !selected.bookmark}
              <SourceSteps source={selected} {live} wide />
            {/if}
          </section>

          {#if attachable}
            <section class="rail-block attach-block">
              <div class="rail-label">{t('source.snapshot')}</div>
              <p class="attach-text">
                {#if attached}
                  {attachedFrom
                    ? t('source.attachedFrom', { from: attachedFrom })
                    : t('source.attachedHere')}
                {:else if selected.bookmark}
                  {t('source.bookmarkTextStart')}
                  <b>{t('source.bookmarkPrint')}</b>
                  {t('source.bookmarkTextEnd')}
                {:else}
                  {selected.steps?.capture?.detail
                    ? t('source.blockedWithDetail', { detail: selected.steps.capture.detail })
                    : t('source.blocked')}
                {/if}
              </p>
              <div class="attach-actions">
                <button
                  class="btn btn--secondary btn--sm btn--action"
                  disabled={attaching}
                  title={attached ? t('source.replaceSnapshot') : t('source.attachSnapshot')}
                  onclick={() => attachInput?.click()}
                >
                  <Icon name="upload" size={13} />
                  <span class="btn-label">
                    {attaching
                      ? t('source.attaching')
                      : attached
                        ? t('source.replaceSnapshot')
                        : t('source.attachSnapshot')}
                  </span>
                </button>
                {#if attached}
                  <button
                    class="btn btn--secondary btn--sm btn--action"
                    disabled={refetching}
                    title={t('source.tryUrlAgain')}
                    onclick={() => void onRefetch()}
                  >
                    <Icon name="refresh" size={13} />
                    <span class="btn-label"
                      >{refetching ? t('source.fetching') : t('source.tryUrlAgain')}</span
                    >
                  </button>
                {/if}
              </div>
              <input
                type="file"
                hidden
                accept=".html,.htm,.md,.markdown,.txt,.pdf"
                bind:this={attachInput}
                onchange={() => void onAttachFile(attachInput?.files ?? null)}
              />
            </section>
          {/if}

          <section class="rail-block">
            <div class="rail-label">{t('source.details')}</div>
            <div class="kv"><span class="k">{t('source.kindLabel')}</span><span class="v">{selected.kind}</span></div>
            <div class="kv"><span class="k">{t('source.sizeLabel')}</span><span class="v">{fmtBytes(selected.bytes)}</span></div>
            <div class="kv">
              <span class="k">{t('source.languageLabel')}</span>
              <span
                class="v"
                class:language-unknown={!selected.language}
                title={selected.language
                  ? t('source.languageHint')
                  : selected.bookmark
                    ? t('source.languageBookmarkHint')
                    : t('source.languageUnknownHint')}
              >{selected.language ||
                (selected.bookmark ? t('source.notIndexed') : t('source.unknown'))}</span>
            </div>
            {#if selected.pages}<div class="kv"><span class="k">{t('source.pagesLabel')}</span><span class="v">{n(selected.pages)}</span></div>{/if}
            {#if !selected.rss && !selected.sitemap}
              <div class="kv"><span class="k">{t('source.outlineLabel')}</span><span class="v">{selected.tree_state}</span></div>
            {/if}
            <div class="kv"><span class="k">{t('source.addedLabel')}</span><span class="v">{relTime(selected.imported_at)}</span></div>
            {#if selected.url}
              <div class="kv">
                <span class="k">{t('source.urlLabel')}</span>
                <span class="v">
                  <a class="rail-link" href={selected.url} target="_blank" rel="noopener noreferrer" title={selected.url}>
                    {hostOf(selected.url) || selected.url}
                  </a>
                </span>
              </div>
            {/if}
            {#if selected.url_status}
              <div class="kv"><span class="k">{t('source.httpLabel')}</span><span class="v">{selected.url_status} · {relTime(selected.url_checked_at)}</span></div>
            {/if}
            <div class="kv"><span class="k">ID</span><span class="v"><CopyId text={selected.hash} label={t('source.idLabel')} display={selected.hash.slice(0, 12)} /></span></div>
            {#if (selected.origin === 'sitemap' || selected.origin === 'rss') && selected.parent_hash}
              <div class="kv">
                <span class="k">{selected.origin === 'rss' ? t('source.feed') : t('source.sitemap')}</span>
                <span class="v">
                  <a class="rail-link" href={sourcePath(selected.parent_hash)}>
                    {selected.origin === 'rss' ? t('source.partOfFeed') : t('source.partOfSitemap')}
                  </a>
                </span>
              </div>
            {/if}
            {#if selected.sitemap}
              <div class="kv">
                <span class="k">{t('source.sitemap')}</span>
                <span class="v">{t('source.sitemapOwns')}</span>
              </div>
            {/if}
            {#if selected.rss}
              <div class="kv">
                <span class="k">{t('source.feed')}</span>
                <span class="v">{t('source.feedOwns')}</span>
              </div>
            {/if}
            {#if selected.from_feed}
              <div class="kv">
                <span class="k">{t('source.copyLabel')}</span>
                <span class="v">{t('source.copyFromFeed')}</span>
              </div>
            {/if}
            {#if selected.original_name?.startsWith('wiki:')}
              <div class="kv">
                <span class="k">wiki</span>
                <span class="v"><a class="rail-link" href={wikiPath(selected.original_name.slice('wiki:'.length))}>{t('source.openPage')}</a></span>
              </div>
            {:else if selected.origin === 'wiki' && selected.wiki_page}
              <div class="kv">
                <span class="k">wiki</span>
                <span class="v"><a class="rail-link" href={wikiPath(selected.wiki_page)}>{t('source.linkedFromPage')}</a></span>
              </div>
            {/if}
            {#if selected.origin || selected.original_name?.startsWith('wiki:')}
              <p class="rail-note">
                {t('source.ownedBy', {
                  owner:
                    selected.origin === 'sitemap'
                      ? t('source.ownerSitemap')
                      : selected.origin === 'rss'
                        ? t('source.ownerFeed')
                        : t('source.ownerWiki'),
                })}
              </p>
            {/if}
          </section>

          <section class="rail-block">
            <div class="rail-label">
              {t('source.tags')}
              {#if !tagsEditing}
                <button
                  class="rail-edit"
                  title={t('source.edit')}
                  aria-label={t('source.edit')}
                  onclick={beginTags}
                >
                  <Icon name="edit" size={11} /> <span class="btn-label">{t('source.edit')}</span>
                </button>
              {/if}
            </div>
            {#if tagsEditing}
              <input
                use:clearable
                class="input tag-input"
                bind:value={tagsDraft}
                placeholder="caching, reading, go"
                aria-label={t('source.tagsAria')}
              />
              <div class="tag-actions">
                <button
                  class="btn btn--primary btn--sm btn--action"
                  disabled={tagsSaving}
                  title={tagsSaving ? t('source.saving') : t('source.save')}
                  onclick={() => void saveTags()}
                >
                  {#if tagsSaving}<span class="spinner"></span>{:else}<Icon name="check" size={13} />{/if}
                  <span class="btn-label">{tagsSaving ? t('source.saving') : t('source.save')}</span>
                </button>
                <button
                  class="btn btn--secondary btn--sm btn--action"
                  title={t('common.cancel')}
                  onclick={() => (tagsEditing = false)}
                >
                  <Icon name="close" size={13} /> <span class="btn-label">{t('common.cancel')}</span>
                </button>
              </div>
              {#if tagsError}<p class="rail-note tag-error">{tagsError}</p>{/if}
            {:else if selected.tags?.length}
              <div class="rail-chips">
                {#each selected.tags as tag (tag)}
                  <span class="chip">{tag}</span>
                {/each}
              </div>
            {:else}
              <p class="rail-note">{t('source.noTags')}</p>
            {/if}
          </section>

          {#if selected.auto_tags?.length}
            <section class="rail-block">
              <div class="rail-label">{t('source.autoTags')}</div>
              <div class="rail-chips">
                {#each selected.auto_tags as tag (tag)}
                  <span class="chip chip--auto" title={t('source.autoTagsTitle')}>{tag}</span>
                {/each}
              </div>
              <p class="rail-note">{t('source.autoTagsNote')}</p>
            </section>
          {/if}

          {#if topics.length > 0}
            <section class="rail-block">
              <div class="rail-label">{t('source.topics')} <span class="rail-count">{n(topics.length)}</span></div>
              <div class="rail-chips">
                {#each topics.slice(0, 5) as topic (topic.name)}
                  <a class="chip" href={`#/claims?topic=${encodeURIComponent(topic.name)}`} title={tn('source.topicClaims', topic.count)}>
                    {topic.name}<span class="chip-count">{n(topic.count)}</span>
                  </a>
                {/each}
              </div>
              {#if topics.length > 5}
                <p class="rail-note">{tn('source.moreTopics', topics.length - 5)}</p>
              {/if}
            </section>
          {/if}

          {#if counts}
            <section class="rail-block">
              <div class="rail-label">{t('source.progress')}</div>
              <div class="kv">
                <span class="k">{t('source.stateLabel')}</span>
                <span class="v" class:working={!!selected.running_step}>
                  {#if selected.running_step}<span class="spinner"></span>{/if}
                  {statusLabel(selected, live)}
                </span>
              </div>
              {#if selected.running_step && counts.msg}
                <div class="kv"><span class="k">{t('source.nowLabel')}</span><span class="v">{counts.phase}: {counts.msg}</span></div>
              {/if}
              {#if progress}
                <div class="progress-lines">
                  <span>{t('source.partsLabel')} <b>{progress.partsDone}/{progress.partsTotal}</b></span>
                  <span>{t('source.claimsLabel')} <b>{progress.claimsDone}/{progress.claimsTotal}</b></span>
                </div>
              {/if}
            </section>
          {/if}
        </aside>
      </div>
    {:else}
      <div class="stack">
        <span class="skeleton" style="height:32px;width:45%"></span>
        <span class="skeleton" style="height:300px"></span>
      </div>
    {/if}
  </div>
{/if}

<style>
  .source-page {
    container: source-page / inline-size;
    padding-top: 20px;
  }

  .source-head {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    margin-bottom: 18px;
    min-width: 0;
  }

  .source-head-icon {
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    flex: none;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    color: var(--muted);
  }

  .source-head-main {
    min-width: 0;
    flex: 1;
  }

  .source-head-main h1 {
    margin: 0 0 3px;
    overflow: hidden;
    color: var(--ink);
    font-size: 20px;
    font-weight: 650;
    letter-spacing: -0.02em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .source-head-sub {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 5px;
    color: var(--faint);
    font-size: 11.5px;
  }

  .source-head-actions {
    display: flex;
    align-items: center;
    gap: 5px;
    flex: none;
  }

  .source-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 280px;
    gap: 32px;
    align-items: start;
  }

  .source-content {
    min-width: 0;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-1);
    overflow: hidden;
  }

  .source-body {
    padding: 22px 24px 30px;
    min-height: 280px;
  }

  .source-rail {
    position: sticky;
    top: 0;
    display: flex;
    flex-direction: column;
    gap: 20px;
    min-width: 0;
  }

  .rail-block {
    min-width: 0;
  }

  .rail-label {
    margin-bottom: 7px;
    color: var(--faint);
    font-size: 10px;
    font-weight: 650;
    letter-spacing: 0.09em;
    text-transform: uppercase;
  }

  .rail-count {
    color: var(--faint);
    font-weight: 400;
    letter-spacing: 0;
    text-transform: none;
  }

  .attach-text {
    margin: 0 0 8px;
    color: var(--muted);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .attach-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .rail-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .rail-note {
    margin: 7px 0 0;
    color: var(--faint);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .rail-link {
    color: var(--accent);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .rail-edit {
    margin-left: 6px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--accent);
    font: inherit;
    letter-spacing: 0;
    text-transform: none;
    cursor: pointer;
  }

  .tag-actions {
    display: flex;
    gap: 5px;
    margin-top: 6px;
  }

  .tag-input {
    height: 32px;
    font-size: 12px;
  }

  .tag-error {
    color: var(--danger);
  }

  .chip--auto {
    border-style: dashed;
    color: var(--muted);
  }

  .language-unknown {
    color: var(--faint);
    font-style: italic;
    cursor: help;
  }

  .progress-lines {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    margin-top: 6px;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10.5px;
  }

  .progress-lines b {
    color: var(--ink-2);
    font-weight: 500;
  }

  .file-head,
  .part-head {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
    min-width: 0;
  }

  .file-head code,
  .part-head code {
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 11.5px;
  }

  .file-head .spacer {
    flex: 1;
  }

  .part-head span {
    overflow: hidden;
    color: var(--muted);
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-notice {
    padding: 4px 0 12px;
    font-size: 12.5px;
  }

  .summary-empty {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
    font-size: 12.5px;
    line-height: 1.6;
  }

  .file-row .chip {
    flex: none;
  }

  .file-intro {
    margin: 0 0 14px;
    font-size: 12.5px;
    line-height: 1.6;
  }

  .summary-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin: 0 0 14px;
  }

  .summary-head .file-intro {
    margin: 0;
  }

  .file-list {
    border-top: 1px solid var(--line);
  }

  .file-row {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 38px;
    border-bottom: 1px solid var(--line);
    color: var(--muted);
    font-size: 12px;
  }

  .file-row.missing {
    opacity: 0.6;
  }

  .file-row .grow {
    flex: 1;
  }

  .file-open {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    min-width: 0;
    padding: 5px 0;
    border: 0;
    background: transparent;
    color: var(--ink-2);
  }

  .file-row .file-open {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-row code {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-open:hover code {
    color: var(--accent);
  }

  .file-open code {
    font-family: var(--font-mono);
    font-size: 11.5px;
  }

  .raw-file {
    margin: 0;
    overflow: auto;
    color: var(--ink-2);
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.65;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .log-file {
    max-height: calc(100dvh - 260px);
  }

  .frame {
    width: 100%;
    height: calc(100dvh - 270px);
    min-height: 420px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg-muted);
  }

  .outline-intro {
    margin-bottom: 12px;
    font-size: 12.5px;
  }

  .outline {
    border-top: 1px solid var(--line);
  }

  .outline-row {
    display: flex;
    align-items: center;
    gap: 9px;
    width: 100%;
    padding: 7px 8px;
    border: 0;
    border-bottom: 1px solid var(--line);
    background: transparent;
    color: inherit;
    text-align: left;
  }

  .outline-row:hover {
    background: var(--bg-hover);
  }

  .outline-row--group {
    color: var(--muted);
  }

  .outline-id {
    flex: none;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .outline-title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .kv .v.working {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--info);
  }

  /* The rail is a reading aid, not a column the content must share at every width. It moves under
     the content once the page can no longer give the content a comfortable 760px beside it (760 +
     280 rail + 32 gap), so the detail surface — and every wide image in it — gets the page's full
     content width while the page's maximum still caps it. */
  @container source-page (max-width: 1072px) {
    .source-layout {
      display: flex;
      flex-direction: column;
      align-items: stretch;
      gap: 22px;
    }

    .source-rail {
      position: static;
      width: 100%;
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 18px;
    }
  }

  @media (max-width: 1000px) {
    .source-layout {
      grid-template-columns: minmax(0, 1fr) 240px;
      gap: 22px;
    }
  }

  @media (max-width: 860px) {
    .source-layout {
      display: flex;
      flex-direction: column;
      align-items: stretch;
    }

    .source-rail {
      position: static;
      width: 100%;
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 18px;
    }

    .source-head-actions {
      width: 100%;
      flex-wrap: wrap;
      justify-content: flex-start;
      margin-top: 2px;
    }
  }

  @media (max-width: 640px) {
    .source-body {
      padding: 16px 15px 22px;
    }

    .source-rail {
      grid-template-columns: minmax(0, 1fr);
    }

    .source-head-icon {
      display: none;
    }

    .frame {
      height: 60vh;
    }
  }
</style>
