<script lang="ts">
  import type { Source } from '../../lib/api/types';
  import { PIPELINE_STEPS, stepsOf, stepTitle } from '../../lib/source-state';

  const { source, size = 'sm' }: { source: Source; size?: 'sm' | 'md' } = $props();
  const steps = $derived(stepsOf(source));
</script>

<span class="lights lights--{size}" aria-label="Pipeline state">
  {#each PIPELINE_STEPS as step (step)}
    <span
      class="light light--{steps[step].state}"
      title={stepTitle(step, steps[step])}
      aria-label={`${step}: ${steps[step].state}`}
    ></span>
  {/each}
</span>

<style>
  .lights {
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }

  .light {
    width: 8px;
    height: 8px;
    border: 1.5px solid var(--line-strong);
    border-radius: 50%;
    background: transparent;
  }

  .lights--md .light {
    width: 10px;
    height: 10px;
  }

  .light--pending {
    border-style: dashed;
  }

  .light--running {
    border-color: var(--info);
    background: var(--info);
    animation: breathe 1.4s var(--ease) infinite;
  }

  .light--ok {
    border-color: var(--success);
    background: var(--success);
  }

  .light--failed {
    border-color: var(--danger);
    background: var(--danger);
  }

  .light--stale {
    border-color: var(--warning);
    background: var(--warning);
  }

  .light--skipped {
    border-style: dotted;
    opacity: 0.45;
  }

  @keyframes breathe {
    50% {
      opacity: 0.35;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .light--running {
      animation: none;
    }
  }
</style>
