<script lang="ts">
  import { onMount, tick } from 'svelte';
  import AddSourceDialog from './components/overlays/AddSourceDialog.svelte';
  import DuplicateWindow from './components/overlays/DuplicateWindow.svelte';
  import SearchPalette from './components/overlays/SearchPalette.svelte';
  import ConfirmDialog from './components/ui/ConfirmDialog.svelte';
  import Toasts from './components/ui/Toasts.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import Topbar from './components/layout/Topbar.svelte';
  import AskView from './components/views/AskView.svelte';
  import AttentionView from './components/views/AttentionView.svelte';
  import BookmarksView from './components/views/BookmarksView.svelte';
  import ClaimsView from './components/views/ClaimsView.svelte';
  import SourcesView from './components/views/SourcesView.svelte';
  import SourceView from './components/views/SourceView.svelte';
  import StatusView from './components/views/StatusView.svelte';
  import WikiView from './components/views/WikiView.svelte';
  import { startSync, resyncAll } from './lib/events/sync';
  import { openSearch } from './lib/overlays';
  import { navigate, router, startRouter } from './lib/router.svelte';
  import { rememberScroll, restoreScroll } from './lib/scroll-memory';
  import { claimWindow } from './lib/single-window';
  import { connection } from './lib/stores/connection.svelte';
  import { claims } from './lib/stores/claims.svelte';
  import { sources } from './lib/stores/sources.svelte';
  import { wiki } from './lib/stores/wiki.svelte';
  import { asks } from './lib/stores/asks.svelte';
  import { initTheme } from './lib/theme.svelte';
  import { closeSidebar, ui } from './lib/ui.svelte';

  const VIEW_LABEL: Record<string, string> = {
    claims: 'Claims',
    wiki: 'Wiki',
    sources: 'Sources',
    source: 'Sources',
    attention: 'Attention',
    bookmarks: 'Bookmarks',
    ask: 'Ask',
    status: 'Status',
  };

  let scroller: HTMLDivElement | undefined = $state();
  let stopSync: (() => void) | undefined;
  let stopRouter: (() => void) | undefined;
  let pageScroll: ReturnType<typeof rememberScroll> | undefined;

  const title = $derived.by(() => {
    const route = router.current;
    if (route.view === 'claims' && claims.selected) return claims.selected.claim;
    if (route.view === 'source' && sources.selected) return sources.selected.title;
    if (route.view === 'wiki' && wiki.selected) return wiki.selected.title;
    if (route.view === 'ask' && asks.selected) return asks.selected.question;
    return VIEW_LABEL[route.view] ?? 'Claims';
  });

  $effect(() => {
    document.title = title ? `${title} · Thought Keeper` : 'Thought Keeper';
  });

  $effect(() => {
    // Read the route key: every navigation is a new place, and the reader's scroll position belongs
    // to the history entry that is now current.
    void router.current.key;
    void tick().then(() => restoreScroll());
    closeSidebar();
  });

  onMount(() => {
    initTheme();
    stopRouter = startRouter();
    stopSync = startSync();
    // The stream is connected before the first reads: an event that arrives while they are in
    // flight is then never missed, and the stores' generation guards make a slow response lose.
    void resyncAll(true);

    if (scroller) pageScroll = rememberScroll(scroller, 'page');

    const onKey = (event: KeyboardEvent): void => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        openSearch();
      }
    };
    window.addEventListener('keydown', onKey);

    claimWindow(() => {
      ui.duplicateWindow = true;
      window.close();
    });

    return () => {
      window.removeEventListener('keydown', onKey);
      stopSync?.();
      stopRouter?.();
      pageScroll?.destroy();
    };
  });
</script>

<div class="app-shell">
  <Sidebar />

  {#if ui.sidebarOpen}
    <button class="sidebar-backdrop" aria-label="Close menu" onclick={closeSidebar}></button>
  {/if}

  <div class="main">
    <Topbar />

    {#if connection.state === 'reconnecting' || connection.state === 'offline'}
      <div class="offline-banner" role="status">
        <span class="dot {connection.state === 'offline' ? 'dot--off' : 'dot--warn'}"></span>
        <span>
          <b>{connection.state === 'offline' ? 'This device is offline.' : 'Not connected to Thought Keeper.'}</b>
          The app may be stopped, or the connection dropped. What you see may be out of date; the
          page reconnects by itself.
        </span>
      </div>
    {/if}

    <div class="content-scroll" bind:this={scroller}>
      {#if router.current.view === 'claims'}
        <ClaimsView />
      {:else if router.current.view === 'wiki'}
        <WikiView />
      {:else if router.current.view === 'sources'}
        <SourcesView />
      {:else if router.current.view === 'source'}
        <SourceView />
      {:else if router.current.view === 'attention'}
        <AttentionView />
      {:else if router.current.view === 'bookmarks'}
        <BookmarksView />
      {:else if router.current.view === 'ask'}
        <AskView />
      {:else if router.current.view === 'status'}
        <StatusView />
      {:else}
        <ClaimsView />
      {/if}
    </div>
  </div>
</div>

<SearchPalette />
<AddSourceDialog />
<ConfirmDialog />
<DuplicateWindow />
<Toasts />
