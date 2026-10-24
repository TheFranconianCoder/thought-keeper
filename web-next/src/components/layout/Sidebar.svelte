<script lang="ts">
  import { connection, connectionLabel } from '../../lib/stores/connection.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import { wiki } from '../../lib/stores/wiki.svelte';
  import { navigate, router } from '../../lib/router.svelte';
  import { openAdd, openSearch } from '../../lib/overlays';
  import { closeSidebar, ui } from '../../lib/ui.svelte';
  import Icon from '../ui/Icon.svelte';

  const active = (name: string): boolean => {
    if (name === 'sources') return router.current.view === 'sources' || router.current.view === 'source';
    return router.current.view === name;
  };

  const go = (hash: string): void => {
    navigate(hash);
    closeSidebar();
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

<aside class="sidebar" class:open={ui.sidebarOpen}>
  <div class="brand">
    <img class="brand-mark" src="/icon.svg" alt="" width="26" height="26" />
    <span class="brand-name">Thought Keeper</span>
  </div>

  <button class="command-button" onclick={openSearch} title="Search claims and sources (Ctrl/Cmd+K)">
    <Icon name="search" size={14} />
    <span>Search…</span>
    <span class="kbd">⌘K</span>
  </button>

  <nav class="nav-section" aria-label="Library">
    <div class="nav-label">Library</div>
    <button class="nav-item" class:active={active('claims')} aria-current={active('claims') ? 'page' : undefined} onclick={() => go('#/claims')}>
      <Icon name="claims" size={15} />
      <span>Claims</span>
      {#if claimCount}<span class="count">{claimCount}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('wiki')} aria-current={active('wiki') ? 'page' : undefined} onclick={() => go('#/wiki')}>
      <Icon name="wiki" size={15} />
      <span>Wiki</span>
      {#if wiki.pages.length}<span class="count">{wiki.pages.length}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('sources')} aria-current={active('sources') ? 'page' : undefined} onclick={() => go('#/sources')}>
      <Icon name="sources" size={15} />
      <span>Sources</span>
      {#if system.status}<span class="count">{system.status.index.sources}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('bookmarks')} aria-current={active('bookmarks') ? 'page' : undefined} onclick={() => go('#/bookmarks')}>
      <Icon name="bookmarks" size={15} />
      <span>Bookmarks</span>
      {#if bookmarkCount}<span class="count">{bookmarkCount}</span>{/if}
    </button>
  </nav>

  <nav class="nav-section" aria-label="Work">
    <div class="nav-label">Work</div>
    <button class="nav-item" class:active={active('ask')} aria-current={active('ask') ? 'page' : undefined} onclick={() => go('#/ask')}>
      <Icon name="ask" size={15} />
      <span>Ask</span>
    </button>
    <button class="nav-item" class:active={active('attention')} aria-current={active('attention') ? 'page' : undefined} onclick={() => go('#/attention')}>
      <Icon name="attention" size={15} />
      <span>Attention</span>
      {#if attentionCount}<span class="count">{attentionCount}</span>{/if}
    </button>
    <button class="nav-item" class:active={active('status')} aria-current={active('status') ? 'page' : undefined} onclick={() => go('#/status')}>
      <Icon name="status" size={15} />
      <span>Status</span>
    </button>
  </nav>

  <div class="sidebar-spacer"></div>

  <div class="sidebar-foot">
    <button class="nav-item" onclick={() => { openAdd(); closeSidebar(); }}>
      <Icon name="plus" size={15} />
      <span>Add source</span>
    </button>
    <div class="connection-line" title="The UI updates through one event stream">
      <span class="dot {dotClass}"></span>
      <span>{connectionLabel()}</span>
    </div>
  </div>
</aside>
