<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
  import { navigate, router } from '../../lib/router.svelte';
  import { connection, connectionLabel } from '../../lib/stores/connection.svelte';
  import { system } from '../../lib/stores/system.svelte';
  import Icon from '../ui/Icon.svelte';

  // The narrow layout's one navigation surface. It is a footer, not a drawer: every lens is one tap
  // away and nothing covers the page. A lens that owns a detail (Sources → source) keeps its tab
  // active while the detail is open.
  const items = $derived([
    { view: 'home', path: '#/home', icon: 'home', label: t('view.home'), detail: [] as string[] },
    { view: 'sources', path: '#/sources', icon: 'sources', label: t('view.sources'), detail: ['source'] },
    { view: 'wiki', path: '#/wiki', icon: 'wiki', label: t('view.wiki'), detail: [] as string[] },
    { view: 'bookmarks', path: '#/bookmarks', icon: 'bookmarks', label: t('view.bookmarks'), detail: [] as string[] },
    { view: 'claims', path: '#/claims', icon: 'claims', label: t('view.claims'), detail: [] as string[] },
    { view: 'ask', path: '#/ask', icon: 'ask', label: t('view.ask'), detail: [] as string[] },
    {
      view: 'attention',
      path: '#/attention',
      icon: 'attention',
      label: t('view.attention'),
      detail: [] as string[],
    },
    { view: 'status', path: '#/status', icon: 'status', label: t('view.status'), detail: [] as string[] },
  ]);

  const attentionCount = $derived(
    (system.status?.index.failed ?? 0) + (system.status?.index.findings ?? 0),
  );

  /** The connection mark uses the same vocabulary as the sidebar's connection line: green live,
   *  orange when reconnecting, grey while the first connection is made, red offline. */
  const connectionTone = $derived(
    connection.state === 'live'
      ? 'live'
      : connection.state === 'offline'
        ? 'off'
        : connection.state === 'connecting'
          ? 'connecting'
          : 'warn',
  );

  const statusTitle = $derived(`${t('view.status')} · ${connectionLabel()}`);

  function isActive(view: string, detail: string[]): boolean {
    const current = router.current.view;
    return current === view || detail.includes(current);
  }
</script>

<nav class="footer-nav" aria-label={t('nav.primary')}>
  {#each items as item (item.view)}
    {@const active = isActive(item.view, item.detail)}
    <button
      class="footer-item"
      class:active
      aria-current={active ? 'page' : undefined}
      aria-label={item.view === 'status' ? statusTitle : item.label}
      title={item.view === 'status' ? statusTitle : item.label}
      onclick={() => navigate(item.path)}
    >
      <span class="footer-icon">
        <Icon name={item.icon} size={19} />
        {#if item.view === 'attention' && attentionCount > 0}
          <span class="footer-dot" aria-hidden="true"></span>
        {/if}
        {#if item.view === 'status'}
          <span class="footer-dot footer-dot--{connectionTone}" aria-hidden="true"></span>
        {/if}
      </span>
      <span class="footer-label">{item.label}</span>
    </button>
  {/each}
</nav>

<style>
  .footer-nav {
    display: none;
  }

  @media (max-width: 900px) {
    .footer-nav {
      display: flex;
      flex: none;
      align-items: stretch;
      justify-content: space-around;
      gap: 2px;
      padding: 5px 6px calc(5px + env(safe-area-inset-bottom, 0px));
      border-top: 1px solid var(--line);
      background: color-mix(in srgb, var(--bg-sidebar) 95%, transparent);
    }

    .footer-item {
      position: relative;
      display: flex;
      flex: 1;
      min-width: 0;
      flex-direction: column;
      align-items: center;
      gap: 2px;
      padding: 4px 2px 3px;
      border: 0;
      border-radius: var(--radius-sm);
      background: transparent;
      color: var(--muted);
      font-size: 10px;
      line-height: 1.2;
      transition: background 0.12s var(--ease), color 0.12s var(--ease);
    }

    .footer-item:hover {
      background: var(--bg-hover);
      color: var(--ink-2);
    }

    .footer-item.active {
      color: var(--accent);
      background: var(--accent-soft);
    }

    .footer-icon {
      position: relative;
      display: inline-flex;
    }

    .footer-label {
      max-width: 100%;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }

    .footer-dot {
      position: absolute;
      top: -2px;
      right: -3px;
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: var(--danger);
      box-shadow: 0 0 0 2px var(--bg-sidebar);
    }

    .footer-dot--live {
      background: var(--success);
    }

    .footer-dot--warn {
      background: var(--warning);
    }

    .footer-dot--off {
      background: var(--danger);
    }

    .footer-dot--connecting {
      background: var(--faint);
    }

    .footer-item.active .footer-dot {
      box-shadow: 0 0 0 2px var(--accent-soft);
    }
  }

  /* Below this width seven German labels no longer fit; the icon and the accessible title remain. */
  @media (max-width: 720px) {
    .footer-label {
      display: none;
    }

    .footer-item {
      padding-top: 7px;
      padding-bottom: 6px;
    }
  }
</style>
