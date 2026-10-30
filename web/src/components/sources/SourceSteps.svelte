<script lang="ts">
  import type { Source } from '../../lib/api/types';
  import type { LiveProgress } from '../../lib/source-state';
  import { progressOf, totalsLabel } from '../../lib/source-state';
  import StepLights from './StepLights.svelte';

  const {
    source,
    live,
    wide = false,
  }: { source: Source; live?: LiveProgress; wide?: boolean } = $props();

  const progress = $derived(progressOf(source, live));
  const totals = $derived(totalsLabel(source, live));
</script>

<div class="source-steps" class:wide>
  <StepLights {source} size={wide ? 'md' : 'sm'} />
  {#if progress}
    <div class="bars" aria-hidden="true">
      <span class="bar">
        <i class="parts" style="width:{progress.parts}%"></i>
      </span>
      <span class="bar">
        <i class="claims" style="width:{progress.claims}%"></i>
      </span>
    </div>
  {/if}
</div>
{#if totals}<div class="totals" class:wide>{totals}</div>{/if}

<style>
  .source-steps {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .source-steps.wide {
    gap: 10px;
  }

  .bars {
    display: flex;
    flex-direction: column;
    gap: 2px;
    width: 38px;
  }

  .wide .bars {
    width: 88px;
    gap: 3px;
  }

  .bar {
    display: block;
    height: 3px;
    overflow: hidden;
    border-radius: 999px;
    background: var(--bg-active);
  }

  .wide .bar {
    height: 4px;
  }

  .bar > i {
    display: block;
    height: 100%;
    border-radius: 999px;
    transition: width 0.35s var(--ease);
  }

  .parts {
    background: var(--accent);
  }

  .claims {
    background: var(--muted);
  }

  .totals {
    margin-top: 5px;
    color: var(--faint);
    font-family: var(--font-mono);
    font-size: 10.5px;
    white-space: nowrap;
  }

  .totals.wide {
    margin-top: 7px;
    font-size: 11.5px;
  }
</style>
