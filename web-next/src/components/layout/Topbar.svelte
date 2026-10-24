<script lang="ts">
  import { system } from '../../lib/stores/system.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { wiki } from '../../lib/stores/wiki.svelte';
  import { asks } from '../../lib/stores/asks.svelte';
  import { router, navigate } from '../../lib/router.svelte';
  import { pipelineLabel } from '../../lib/source-state';
  import { cycleTheme, themeIcon, themeLabel } from '../../lib/theme.svelte';
  import { toggleSidebar } from '../../lib/ui.svelte';
  import Icon from '../ui/Icon.svelte';

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

  const crumb = $derived.by(() => {
    const route = router.current;
    if (route.view === 'claims' && claims.selected) {
      return { parent: 'Claims', current: claims.selected.topic || claims.selected.claim };
    }
    if (route.view === 'source') {
      return { parent: 'Sources', current: sources.selected?.title || route.id.slice(0, 12) };
    }
    if (route.view === 'wiki') {
      if (route.id === 'new') return { parent: 'Wiki', current: 'New page' };
      if (wiki.selected) return { parent: 'Wiki', current: wiki.selected.title };
      return { parent: '', current: 'Wiki' };
    }
    if (route.view === 'ask' && asks.selected) {
      return { parent: 'Ask', current: asks.selected.question };
    }
    return { parent: '', current: VIEW_LABEL[route.view] ?? 'Claims' };
  });

  const activity = $derived(
    pipelineLabel(system.pipeline) ||
      (system.banner ? `${system.banner.op || 'working'}…` : ''),
  );
</script>

<header class="topbar">
  <button class="icon-btn menu-button" title="Menu" onclick={toggleSidebar}>
    <Icon name="menu" size={18} />
  </button>

  <div class="crumbs" aria-label="Breadcrumb">
    {#if crumb.parent}
      <span class="crumb-parent">{crumb.parent}</span>
      <span class="sep">/</span>
    {/if}
    <span class="current">{crumb.current}</span>
  </div>

  <span class="topbar-spacer"></span>

  <div class="topbar-actions">
    {#if activity}
      <button
        class="activity-chip"
        title="Open the job queue"
        onclick={() => navigate('#/status')}
      >
        <span class="spinner"></span>
        <span class="truncate">{activity}</span>
      </button>
    {/if}
    <button class="icon-btn" title={themeLabel()} onclick={cycleTheme}>
      <Icon name={themeIcon()} size={17} />
    </button>
  </div>
</header>
