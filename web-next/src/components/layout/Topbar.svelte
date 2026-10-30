<script lang="ts">
  import { system } from '../../lib/stores/system.svelte';
  import { claims } from '../../lib/stores/claims.svelte';
  import { sources } from '../../lib/stores/sources.svelte';
  import { wiki } from '../../lib/stores/wiki.svelte';
  import { asks } from '../../lib/stores/asks.svelte';
  import { cycleLocale, localeLabel, t } from '../../lib/i18n/index.svelte';
  import { router, navigate } from '../../lib/router.svelte';
  import { pipelineLabel } from '../../lib/source-state';
  import { cycleTheme, themeIcon, themeLabel } from '../../lib/theme.svelte';
  import Icon from '../ui/Icon.svelte';

  const viewLabel = (view: string): string => {
    const key = `view.${view}`;
    const label = t(key);
    return label === key ? view : label;
  };

  const crumb = $derived.by(() => {
    const route = router.current;
    if (route.view === 'claims' && claims.selected) {
      return { parent: t('view.claims'), current: claims.selected.topic || claims.selected.claim };
    }
    if (route.view === 'source') {
      return { parent: t('view.sources'), current: sources.selected?.title || route.id.slice(0, 12) };
    }
    if (route.view === 'wiki') {
      if (route.id === 'new') return { parent: t('view.wiki'), current: t('wiki.newPage') };
      if (wiki.selected) return { parent: t('view.wiki'), current: wiki.selected.title };
      return { parent: '', current: t('view.wiki') };
    }
    if (route.view === 'ask' && asks.selected) {
      return { parent: t('view.ask'), current: asks.selected.question };
    }
    return { parent: '', current: viewLabel(route.view) };
  });

  const activity = $derived(
    pipelineLabel(system.pipeline) ||
      (system.banner ? `${system.banner.op || t('shell.activity.working')}…` : ''),
  );
</script>

<header class="topbar">
  <div class="crumbs" aria-label={t('nav.breadcrumb')}>
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
        title={t('shell.activity.title')}
        onclick={() => navigate('#/status')}
      >
        <span class="spinner"></span>
        <span class="truncate">{activity}</span>
      </button>
    {/if}
    <button class="icon-btn" title={t('language.title', { language: localeLabel() })} onclick={cycleLocale}>
      <Icon name="language" size={17} />
    </button>
    <button class="icon-btn" title={themeLabel()} onclick={cycleTheme}>
      <Icon name={themeIcon()} size={17} />
    </button>
  </div>
</header>
