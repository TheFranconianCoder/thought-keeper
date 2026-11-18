<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
  import { tooltip } from '../../lib/tooltip.svelte';
</script>

<!-- The shared "new" mark: one component, so the Sources list and the Start card can never disagree
     about what the tag says. -->
<span class="tag tag--new" use:tooltip={t('sources.tagNewTitle')}>{t('sources.tagNew')}</span>

<style>
  .tag--new {
    flex: none;
    gap: 3px;
    background: var(--accent-soft);
    color: var(--accent);
  }
</style>
