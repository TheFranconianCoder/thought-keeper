<script lang="ts">
  import type { Source } from '../../lib/api/types';
  import { interestChipSlugs } from '../../lib/source-state';
  import { interests } from '../../lib/stores/interests.svelte';
  import Icon from './Icon.svelte';

  // ONE meaning everywhere it appears: this source is in the read-later queue because of that rule.
  // A finished entry drops the chip — the provenance of a read entry lives in the source's own rail,
  // not as a label on a list row. A rule the reader deleted leaves a dangling slug in the record; it
  // is not rendered, exactly as the queue row drops its label.
  const { source }: { source: Source } = $props();

  const slugs = $derived(interestChipSlugs(source, (slug) => !!interests.bySlug(slug)));
  const names = $derived(slugs.map((slug) => interests.nameOf(slug)));
</script>

{#if slugs.length > 0}
  <span class="tag tag--interest" title={names.join(', ')}>
    <Icon name="target" size={10} /> {names[0]}{#if slugs.length > 1}+{slugs.length - 1}{/if}
  </span>
{/if}

<style>
  .tag--interest {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 4px;
    border-color: transparent;
    background: var(--accent-soft);
    color: var(--accent);
  }
</style>
