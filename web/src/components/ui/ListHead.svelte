<script lang="ts">
  import type { Snippet } from 'svelte';
  import { n } from '../../lib/i18n/index.svelte';

  // One head for every library list: the title, its count and the lens's single action on one row,
  // then whatever the lens needs below it (search, filters, chip cloud). The row never wraps — the
  // title truncates — so the action sits in the same place in every language and at every width.
  let {
    title,
    count,
    action,
    children,
  }: {
    title: string;
    count?: number | string | null;
    action?: Snippet;
    children?: Snippet;
  } = $props();
</script>

<div class="list-head">
  <div class="head-row">
    <h1>{title}</h1>
    {#if count !== undefined && count !== null}
      <span class="count">{typeof count === 'number' ? n(count) : count}</span>
    {/if}
    <span class="spacer"></span>
    {@render action?.()}
  </div>
  {@render children?.()}
</div>
