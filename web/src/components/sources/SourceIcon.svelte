<script lang="ts">
  import { api } from '../../lib/api';
  import Icon from '../ui/Icon.svelte';

  const { kind, hash, size = 16 }: { kind: string; hash: string; size?: number } = $props();

  let failed = $state(false);
  $effect(() => {
    void hash;
    failed = false;
  });

  const glyph = $derived(
    kind === 'url'
      ? 'link'
      : kind === 'pdf'
        ? 'filePdf'
        : kind === 'epub'
          ? 'book'
          : kind === 'txt'
            ? 'fileText'
            : kind === 'md'
              ? 'file'
              : 'file',
  );
</script>

{#if kind === 'url' && !failed}
  <img
    class="favicon"
    src={api.faviconURL(hash)}
    alt=""
    loading="lazy"
    onerror={() => (failed = true)}
  />
{:else}
  <Icon name={glyph} {size} />
{/if}

<style>
  .favicon {
    width: 16px;
    height: 16px;
    flex: none;
    border-radius: 3px;
    object-fit: contain;
  }
</style>
