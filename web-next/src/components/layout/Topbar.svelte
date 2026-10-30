<script lang="ts">
  import { system } from '../../lib/stores/system.svelte';
  import { cycleLocale, localeLabel, t } from '../../lib/i18n/index.svelte';
  import { navigate } from '../../lib/router.svelte';
  import { pipelineLabel } from '../../lib/source-state';
  import { cycleTheme, themeIcon, themeLabel } from '../../lib/theme.svelte';
  import { openSearch } from '../../lib/overlays';
  import Icon from '../ui/Icon.svelte';

  const activity = $derived(
    pipelineLabel(system.pipeline) ||
      (system.banner ? `${system.banner.op || t('shell.activity.working')}…` : ''),
  );
</script>

<header class="topbar">
  <!-- Narrow layout: the sidebar is gone, so the bar carries the brand and the search becomes an
       icon beside the other utilities. Wide: the sidebar carries the brand and the bar carries the
       global search. Orientation comes from the page title, never from a breadcrumb here. -->
  <div class="topbar-brand">
    <img class="topbar-mark" src="/icon.svg" alt="" width="24" height="24" />
    <span class="topbar-brand-name">{t('app.brand')}</span>
  </div>

  <button class="topbar-search" title={t('nav.searchTitle')} aria-label={t('nav.searchTitle')} onclick={openSearch}>
    <Icon name="search" size={15} />
    <span class="topbar-search-label">{t('nav.search')}</span>
    <span class="kbd">⌘K</span>
  </button>

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
    <button class="icon-btn topbar-search-icon" title={t('nav.searchTitle')} aria-label={t('nav.searchTitle')} onclick={openSearch}>
      <Icon name="search" size={17} />
    </button>
    <button class="icon-btn" title={t('language.title', { language: localeLabel() })} onclick={cycleLocale}>
      <Icon name="language" size={17} />
    </button>
    <button class="icon-btn" title={themeLabel()} onclick={cycleTheme}>
      <Icon name={themeIcon()} size={17} />
    </button>
  </div>
</header>
