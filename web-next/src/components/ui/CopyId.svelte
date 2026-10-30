<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
  import { toast } from '../../lib/toast.svelte';
  import Icon from './Icon.svelte';

  const {
    text,
    label = 'ID',
    display,
  }: { text: string; label?: string; display?: string } = $props();

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(text);
      toast(t('copy.copied', { label }), 'success');
    } catch {
      toast(t('copy.failed', { label: label.toLowerCase() }), 'error');
    }
  }
</script>

<button class="copy-id" title={t('copy.title', { label })} onclick={() => void copy()}>
  <code>{display ?? text}</code>
  <Icon name="copy" size={11} />
</button>

<style>
  .copy-id {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    max-width: 100%;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--muted);
  }

  .copy-id:hover {
    color: var(--ink);
  }

  code {
    overflow: hidden;
    font-family: var(--font-mono);
    font-size: 10.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
