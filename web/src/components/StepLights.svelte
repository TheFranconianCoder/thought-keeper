<script lang="ts">
	import type { Source } from '../lib/types';
	import { PIPELINE_STEPS, stepTitle, stepsOf } from '../lib/sourceStatus';

	/** StepLights draws the four steps of the pipeline as a traffic light: capture → outline →
	 *  distill → cluster. The first three are document steps (they run for this source), the fourth is
	 *  the association step (it runs over everything, and only while no document step runs). Empty = not done, blue = running, green = ok, red = failed (and a
	 *  failure blocks the step after it), grey = does not apply (a bookmark).
	 *
	 *  This is the ONE place the states are drawn; the source row and the overlay share it, so the
	 *  row and the detail view cannot disagree about what a source has been through. */
	let { source }: { source: Source } = $props();
	const steps = $derived(stepsOf(source));
</script>

<span class="lights">
	{#each PIPELINE_STEPS as step (step)}
		<span class="light {steps[step].state}" title={stepTitle(step, steps[step])} aria-label={step}>
			<i>{step[0].toUpperCase()}</i>
		</span>
	{/each}
</span>

<style>
	.lights {
		display: inline-flex;
		align-items: center;
		gap: 3px;
	}

	.light {
		position: relative;
		width: 14px;
		height: 14px;
		border-radius: 50%;
		border: 1.5px solid var(--border-strong);
		background: transparent;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		cursor: help;
	}

	.light i {
		font-style: normal;
		font-size: 8px;
		font-family: var(--font-mono);
		line-height: 1;
		color: transparent;
	}

	.light.pending {
		border-style: dashed;
	}

	.light.running {
		border-color: #2563eb;
		background: #2563eb;
		animation: pulse 1.4s var(--ease) infinite;
	}

	.light.running i,
	.light.ok i,
	.light.failed i,
	.light.stale i {
		color: #fff;
	}

	.light.ok {
		border-color: #15803d;
		background: #15803d;
	}

	.light.failed {
		border-color: var(--danger, #b4241f);
		background: var(--danger, #b4241f);
	}

	.light.stale {
		border-color: #b45309;
		background: #b45309;
	}

	.light.skipped {
		border-style: dotted;
		opacity: 0.45;
	}

	@keyframes pulse {
		0%,
		100% {
			opacity: 1;
		}
		50% {
			opacity: 0.5;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.light.running {
			animation: none;
		}
	}
</style>
