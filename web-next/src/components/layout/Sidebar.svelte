<script lang="ts">
  import { connection, connectionLabel } from '../../lib/stores/connection.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { wiki } from '../../lib/stores/wiki.svelte';
  import { t } from '../../lib/i18n/index.svelte';
  import { navigate, router } from '../../lib/router.svelte';
  import { openAdd, openSearch } from '../../lib/overlays';
  import Icon from '../ui/Icon.svelte';

  const active = (name: string): boolean => {
    if (name === 'sources') return router.current.view === 'sources' || router.current.view === 'source';
    return router.current.view === name;
  };

  const go = (hash: string): void => {
    navigate(hash);
  };

  // The badges are VAULT-WIDE numbers from the status report. Deriving them from the loaded list
  // made them grow page by page (Bookmarks briefly "1") and made every lens that reloaded the list
  // move the other badges too.
  const bookmarkCount = $derived(system.status?.index.bookmarks ?? 0);
  const attentionCount = $derived(
    (system.status?.index.failed ?? 0) + (system.status?.index.findings ?? 0),
  );
  const claimCount = $derived(system.status?.index.atoms ?? claims.total);

  const dotClass = $derived(
    connection.state === 'live'
      ? 'dot--live'
      : connection.state === 'offline'
        ? 'dot--off'
        : connection.state === 'connecting'
          ? ''
          : 'dot--warn',
  );
</script>

<aside class="sidebar">
  <div class="brand">
    <img class="brand-mark" src="/icon.svg" alt="" width="26" height="26" />
    <span class="brand-name">{t('app.brand')}</span>
  </div>

  <button class="command-button" onclick={openSearch} title={t('nav.searchTitle')}>
    <Icon name="search" size={14} />
    <span>{t('nav.search')}</span>
    <span class="kbd">⌘K</span>
  </button>

  <nav class="nav-section nav-section--home" aria-label={t('view.home')}>
    <button class="nav-item" class:active={active('home')} aria-current={active('home') ? 'page' : undefined} onclick={() => go('#/home')}>
      <Icon name="home" size={15} />
      <span>{t('view.home')}</span>
    </button>
  </nav>

  <nav class="nav-section" aria-label={t('nav.library')}>
    <div class="nav-label">{t('nav.library')}</div>
    <button class="nav-item" class:active={active('sources')} aria-current={active('sources') ? 'page' : undefined} onclick={() => go('#/sources')}>
      <Icon name="sources" size={15} />
      <span>{t('view.sources')}</span>
      {#if system.status}<span class="count">{system.status.index.sources}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('wiki')} aria-current={active('wiki') ? 'page' : undefined} onclick={() => go('#/wiki')}>
      <Icon name="wiki" size={15} />
      <span>{t('view.wiki')}</span>
      {#if wiki.pages.length}<span class="count">{wiki.pages.length}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('bookmarks')} aria-current={active('bookmarks') ? 'page' : undefined} onclick={() => go('#/bookmarks')}>
      <Icon name="bookmarks" size={15} />
      <span>{t('view.bookmarks')}</span>
      {#if bookmarkCount}<span class="count">{bookmarkCount}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('claims')} aria-current={active('claims') ? 'page' : undefined} onclick={() => go('#/claims')}>
      <Icon name="claims" size={15} />
      <span>{t('view.claims')}</span>
      {#if claimCount}<span class="count">{claimCount}</span>{/if}
    </button>
  </nav>

  <nav class="nav-section" aria-label={t('nav.work')}>
    <div class="nav-label">{t('nav.work')}</div>
    <button class="nav-item" class:active={active('ask')} aria-current={active('ask') ? 'page' : undefined} onclick={() => go('#/ask')}>
      <Icon name="ask" size={15} />
      <span>{t('view.ask')}</span>
    </button>
    <button class="nav-item" class:active={active('attention')} aria-current={active('attention') ? 'page' : undefined} onclick={() => go('#/attention')}>
      <Icon name="attention" size={15} />
      <span>{t('view.attention')}</span>
      {#if attentionCount}<span class="count">{attentionCount}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('status')} aria-current={active('status') ? 'page' : undefined} onclick={() => go('#/status')}>
      <Icon name="status" size={15} />
      <span>{t('view.status')}</span>
    </button>
  </nav>

  <div class="sidebar-spacer"></div>

  <div class="sidebar-foot">
    <button class="nav-item" onclick={() => openAdd()}>
      <Icon name="plus" size={15} />
      <span>{t('nav.addSource')}</span>
    </button>
    <div class="connection-line" title={t('connection.title')}>
      <span class="dot {dotClass}"></span>
      <span>{connectionLabel()}</span>
    </div>
  </div>
</aside>
