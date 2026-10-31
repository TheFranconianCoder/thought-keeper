<script lang="ts">
  import { dismissToast, toasts } from '../../lib/toast.svelte';
  import { t } from '../../lib/i18n/index.svelte';
  import Icon from './Icon.svelte';

  const iconOf = (tone: string): string =>
    tone === 'success' ? 'check' : tone === 'warning' ? 'warning' : tone === 'error' ? 'error' : 'info';
</script>

<div class="toasts" aria-live="polite" aria-atomic="false">
  {#each toasts.items as item (item.id)}
    <button class="toast toast--{item.tone}" onclick={() => dismissToast(item.id)} title={t('common.dismiss')}>
      <Icon name={iconOf(item.tone)} size={15} />
      <span>{item.message}</span>
      <Icon name="close" size={12} class="toast-close" />
    </button>
  {/each}
</div>

<style>
  .toasts {
    position: fixed;
    right: 20px;
    /* The scroll-to-top button owns the content corner below the stack; a toast never covers it.
       The inset keeps the stack above the narrow layout's footer navigation on a device with a
       home indicator. */
    bottom: calc(74px + env(safe-area-inset-bottom, 0px));
    z-index: 80;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 7px;
    max-width: min(440px, calc(100vw - 32px));
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    max-width: 100%;
    padding: 9px 11px;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-2);
    color: var(--ink-2);
    font-size: 12.5px;
    line-height: 1.45;
    text-align: left;
    pointer-events: auto;
    animation: toast-in 0.18s var(--ease);
  }

  .toast--success {
    border-color: color-mix(in srgb, var(--success) 35%, var(--line));
  }

  .toast--success :global(.ico:first-child) {
    color: var(--success);
  }

  .toast--warning :global(.ico:first-child) {
    color: var(--warning);
  }

  .toast--error {
    border-color: color-mix(in srgb, var(--danger) 35%, var(--line));
  }

  .toast--error :global(.ico:first-child) {
    color: var(--danger);
  }

  .toast :global(.ico:first-child) {
    color: var(--accent);
    margin-top: 1px;
  }

  .toast :global(.toast-close) {
    margin-left: 4px;
    color: var(--faint);
  }

  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(5px);
    }
  }
</style>
