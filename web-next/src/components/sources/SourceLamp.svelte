<script lang="ts">
  import type { Source } from '../../lib/api/types';
  import type { LiveProgress } from '../../lib/source-state';
  import { overallPercent, overallState, statusLabel } from '../../lib/source-state';

  // ONE lamp for the whole derivation: the ring is the slower of the two progress dimensions, the
  // colour is the aggregate state. The narrow Sources list trades four step lights for this.
  const { source, live }: { source: Source; live?: LiveProgress } = $props();

  const state = $derived(overallState(source, live));
  const percent = $derived(overallPercent(source, live));
  const label = $derived(statusLabel(source, live));
</script>

<span
  class="lamp lamp--{state}"
  style="--pct:{percent}"
  role="img"
  title={label}
  aria-label={label}
>
  <span class="lamp-core"></span>
</span>

<style>
  .lamp {
    --ring: var(--faint);
    display: inline-grid;
    flex: none;
    place-items: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: conic-gradient(var(--ring) calc(var(--pct) * 1%), var(--line) 0);
    color: var(--ring);
  }

  .lamp-core {
    width: 7px;
    height: 7px;
    border: 1.5px solid var(--ring);
    border-radius: 50%;
    background: var(--bg-elevated);
  }

  .lamp--running {
    --ring: var(--info);
    animation: breathe 1.4s var(--ease) infinite;
  }

  .lamp--ok {
    --ring: var(--success);
  }

  .lamp--failed {
    --ring: var(--danger);
  }

  .lamp--stale {
    --ring: var(--warning);
  }

  .lamp--pending {
    --ring: var(--line-strong);
  }

  .lamp--skipped {
    --ring: var(--faint);
    opacity: 0.55;
  }

  @keyframes breathe {
    50% {
      opacity: 0.4;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .lamp--running {
      animation: none;
    }
  }
</style>
