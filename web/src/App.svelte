<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import AddSourceDialog from './components/overlays/AddSourceDialog.svelte';
  import AssistantDialog from './components/overlays/AssistantDialog.svelte';
  import ConfigDialog from './components/overlays/ConfigDialog.svelte';
  import DuplicateWindow from './components/overlays/DuplicateWindow.svelte';
  import InterestsDialog from './components/overlays/InterestsDialog.svelte';
  import SearchPalette from './components/overlays/SearchPalette.svelte';
  import ConfirmDialog from './components/ui/ConfirmDialog.svelte';
  import Toasts from './components/ui/Toasts.svelte';
  import FooterNav from './components/layout/FooterNav.svelte';
  import Sidebar from './components/layout/Sidebar.svelte';
  import Topbar from './components/layout/Topbar.svelte';
  import ScrollTop from './components/ui/ScrollTop.svelte';
  import AskView from './components/views/AskView.svelte';
  import AttentionView from './components/views/AttentionView.svelte';
  import BookmarksView from './components/views/BookmarksView.svelte';
  import ClaimsView from './components/views/ClaimsView.svelte';
  import HomeView from './components/views/HomeView.svelte';
  import SourcesView from './components/views/SourcesView.svelte';
  import SourceView from './components/views/SourceView.svelte';
  import StatusView from './components/views/StatusView.svelte';
  import WikiView from './components/views/WikiView.svelte';
  import { startSync, resyncAll } from './lib/events/sync';
  import { adoptServerDefault, initLocale, t } from './lib/i18n/index.svelte';
  import { openAssistant, openSearch } from './lib/overlays';
  import { navigate, router, startRouter } from './lib/router.svelte';
  import { rememberScroll, restoreScroll } from './lib/scroll-memory';
  import { scrollTopTarget } from './lib/scroll-top.svelte';
  import { claimWindow } from './lib/single-window';
  import { connection } from './lib/stores/connection.svelte';
  import { claims } from './lib/stores/claims.svelte';
  import { sources } from './lib/stores/sources.svelte';
  import { system } from './lib/stores/system.svelte';
  import { wiki } from './lib/stores/wiki.svelte';
  import { asks } from './lib/stores/asks.svelte';
  import { initTheme } from './lib/theme.svelte';
  import { markSourceOpened, newSources } from './lib/new-sources.svelte';
  import { startUpdateWatch } from './lib/update.svelte';
  import { ui } from './lib/ui.svelte';

  const viewLabel = (view: string): string => {
    const key = `view.${view}`;
    const label = t(key);
    return label === key ? view : label;
  };

  let scroller: HTMLDivElement | undefined = $state();
  let stopSync: (() => void) | undefined;
  let stopRouter: (() => void) | undefined;
  let stopUpdates: (() => void) | undefined;
  let pageScroll: ReturnType<typeof rememberScroll> | undefined;

  const title = $derived.by(() => {
    const route = router.current;
    if (route.view === 'claims' && claims.selected) return claims.selected.claim;
    if (route.view === 'source' && sources.selected) return sources.selected.title;
    if (route.view === 'wiki' && wiki.selected) return wiki.selected.title;
    if (route.view === 'ask' && asks.selected) return asks.selected.question;
    return viewLabel(route.view);
  });

  $effect(() => {
    document.title = title ? t('app.documentTitle', { title }) : t('app.brand');
  });

  $effect(() => {
    const configured = system.status?.language?.ui;
    if (configured) adoptServerDefault(configured);
  });

  $effect(() => {
    // Read the route key: every navigation is a new place, and the reader's scroll position belongs
    // to the history entry that is now current.
    void router.current.key;
    void tick().then(() => restoreScroll());
  });

  $effect(() => {
    // Opening a source is the reader's answer for that one row: its own "new" mark drops at once,
    // whichever way the page was reached (a row, a citation, a deep link, back). The shared watermark
    // is not touched: Start and Sources still acknowledge the whole batch.
    const route = router.current;
    if (route.view !== 'source' || !route.id) return;
    untrack(() => markSourceOpened(route.id));
  });

  onMount(() => {
    initLocale();
    initTheme();
    stopRouter = startRouter();
    stopSync = startSync();
    stopUpdates = startUpdateWatch();
    // The stream is connected before the first reads: an event that arrives while they are in
    // flight is then never missed, and the stores' generation guards make a slow response lose.
    void resyncAll(true);

    if (scroller) pageScroll = rememberScroll(scroller, 'page');

    const onKey = (event: KeyboardEvent): void => {
      if (!(event.metaKey || event.ctrlKey)) return;
      const key = event.key.toLowerCase();
      if (key === 'k') {
        event.preventDefault();
        openSearch();
      } else if (key === 'j') {
        // The assistant's field, one key away from the search: both are ways INTO the app, and the
        // difference is that one finds and the other drives.
        event.preventDefault();
        openAssistant();
      }
    };
    window.addEventListener('keydown', onKey);

    claimWindow(
      () => {
        ui.duplicateWindow = true;
        window.close();
      },
      () => {
        // Only the window that won the election may acknowledge the batch; a duplicate that is
        // about to close must not consume the reader's arrivals.
        newSources.primary = true;
      },
    );

    return () => {
      window.removeEventListener('keydown', onKey);
      stopSync?.();
      stopRouter?.();
      stopUpdates?.();
      pageScroll?.destroy();
    };
  });
</script>

<div class="app-shell">
  <Sidebar />

  <div class="main">
    <Topbar />

    {#if connection.state === 'reconnecting' || connection.state === 'offline'}
      <div class="offline-banner" role="status">
        <span class="dot {connection.state === 'offline' ? 'dot--off' : 'dot--warn'}"></span>
        <span>
          <b>{connection.state === 'offline' ? t('app.offlineTitle') : t('app.notConnectedTitle')}</b>
          {t('app.offlineBody')}
        </span>
      </div>
    {/if}

    <div class="content-shell">
      <div class="content-scroll" bind:this={scroller} use:scrollTopTarget>
        {#if router.current.view === 'home'}
          <HomeView />
        {:else if router.current.view === 'claims'}
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
          <HomeView />
        {/if}
      </div>
      <ScrollTop />
    </div>

    <FooterNav />
  </div>
</div>

<SearchPalette />
<AssistantDialog />
<AddSourceDialog />
<InterestsDialog />
<ConfigDialog />
<ConfirmDialog />
<DuplicateWindow />
<Toasts />
