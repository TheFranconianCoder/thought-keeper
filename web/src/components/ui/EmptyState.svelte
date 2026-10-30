<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  const {
    icon = 'info',
    title,
    text,
    children,
  }: { icon?: string; title: string; text?: string; children?: Snippet } = $props();
</script>

<div class="empty">
  <span class="empty__icon"><Icon name={icon} size={19} /></span>
  <div class="empty__title">{title}</div>
  {#if text}<p class="empty__text">{text}</p>{/if}
  {#if children}{@render children()}{/if}
</div>
