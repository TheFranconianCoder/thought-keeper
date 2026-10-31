<script lang="ts">
  import { n, t } from '../../lib/i18n/index.svelte';

  interface Props {
    hidden: number;
    expanded: boolean;
    truncated: boolean;
    ontoggle: () => void;
  }

  const { hidden, expanded, truncated, ontoggle }: Props = $props();
</script>

{#if hidden > 0 || (expanded && truncated)}
  <div class="cloud-more">
    <button
      class="chip cloud-more__chip"
      type="button"
      aria-expanded={expanded}
      onclick={ontoggle}
    >
      {#if hidden > 0}
        +{n(hidden)} {t('cloud.more')}
      {:else}
        {t('cloud.less')}
      {/if}
    </button>
  </div>
{/if}

<style>
  /* The tail's door sits under the cloud, outside its scroll area, so it is always reachable. */
  .cloud-more {
    display: flex;
    margin: -5px 0 10px;
  }

  .cloud-more__chip {
    border-style: dashed;
    color: var(--muted);
  }

  .cloud-more__chip:hover {
    border-color: var(--accent);
    color: var(--ink);
  }
</style>
