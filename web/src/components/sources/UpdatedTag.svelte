<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
</script>

<!-- The shared "updated" mark: one component, so the Sources list and the source page can never
     disagree about what the tag says. It means the stored snapshot's content changed since the
     reader's last visit — not that the page was merely checked. -->
<span class="tag tag--updated" title={t('sources.tagUpdatedTitle')}>{t('sources.tagUpdated')}</span>

<style>
  .tag--updated {
    flex: none;
    gap: 3px;
    background: var(--info-soft);
    color: var(--info);
  }
</style>
