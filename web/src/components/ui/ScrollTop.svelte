<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
  import { scrollToTop, scrollTop } from '../../lib/scroll-top.svelte';
  import Icon from './Icon.svelte';
</script>

<!-- One button serves every list. Its parent (.content-shell) positions it over the page's scroll
     area, so on the narrow layout it sits above the footer navigation, and a split view's list pane
     is reached too because the button follows whichever container the reader scrolled. -->
{#if scrollTop.visible}
  <button
    class="scroll-top"
    title={t('common.scrollTop')}
    aria-label={t('common.scrollTop')}
    onclick={scrollToTop}
  >
    <Icon name="arrowUp" size={17} />
  </button>
{/if}

<style>
  .scroll-top {
    position: absolute;
    right: 20px;
    bottom: 20px;
    z-index: 20;
    display: grid;
    place-items: center;
    width: 38px;
    height: 38px;
    padding: 0;
    border: 1px solid var(--line);
    border-radius: 50%;
    background: var(--bg-elevated);
    color: var(--ink-2);
    box-shadow: var(--shadow-1);
    transition:
      background 0.12s var(--ease),
      color 0.12s var(--ease),
      transform 0.12s var(--ease);
  }

  .scroll-top:hover {
    background: var(--bg-hover);
    color: var(--ink);
  }

  .scroll-top:active {
    transform: translateY(1px);
  }

  .scroll-top:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 3px;
  }

  @media (max-width: 640px) {
    .scroll-top {
      right: 14px;
      bottom: 14px;
      width: 36px;
      height: 36px;
    }
  }
</style>
