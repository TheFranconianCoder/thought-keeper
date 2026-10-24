<script lang="ts">
	import type { Source } from '../lib/types';
	import StepLights from './StepLights.svelte';
	import { progressOf, totalsLabel } from '../lib/sourceStatus';

	/** SourceSteps is everything a source's pipeline state consists of, in one block and in one
	 *  order: the four step lights with the two progress bars beside them, and below both what the
	 *  source is made of.
	 *
	 *  The two bars are the two phases of the derivation — PARTS (how much of the document has been
	 *  distilled) and CLAIMS (how many of the claims have reached a page). They are stacked, sit to the
	 *  LEFT of the lights and are always shown together; a source that is FINISHED shows none. Below
	 *  them the totals say what the source is made of — how many parts and claims — and they are always
	 *  shown, because that is not a progress.
	 *
	 *  The list row and the detail overlay share this component, so they cannot disagree about what a
	 *  source has been through. `wide` is the overlay's rail, where the block has the full column. */
	let { source, wide = false }: { source: Source; wide?: boolean } = $props();
	const prog = $derived(progressOf(source));
</script>

<div class="state">
	{#if prog}
		<div class="bars" class:wide>
			<!-- One track per dimension; the tooltip names it, so two bars can never be mistaken for
			     one another. -->
			<span class="bar parts" title="parts — {prog.partsDone} of {prog.partsTotal} distilled"
				><i style="width:{prog.parts}%"></i></span
			>
			<span class="bar claims" title="claims — {prog.claimsDone} of {prog.claimsTotal} on a page"
				><i style="width:{prog.claims}%"></i></span
			>
		</div>
	{/if}
	<StepLights {source} />
</div>

<div class="totals" class:wide>{totalsLabel(source)}</div>

<style>
	.state {
		display: flex;
		align-items: center;
		gap: 8px;
	}

	.bars {
		display: flex;
		flex-direction: column;
		gap: 2px;
		width: 42px;
	}

	.bars.wide {
		width: 84px;
		gap: 3px;
	}

	.bar {
		display: block;
		height: 3px;
		border-radius: 999px;
		background: var(--industrial);
		overflow: hidden;
	}

	.bars.wide .bar {
		height: 4px;
	}

	.bar > i {
		display: block;
		height: 100%;
		border-radius: 999px;
		transition: width 0.3s ease;
	}

	/* The two dimensions in two colours: the accent for what is being read (parts), a quieter slate
	   for what has reached a page (claims) — neither is a state light, so neither borrows its
	   colour. */
	.bar.parts > i {
		background: var(--accent);
	}

	.bar.claims > i {
		background: var(--muted);
	}

	.totals {
		margin-top: 5px;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--faint);
		white-space: nowrap;
	}

	.totals.wide {
		margin-top: 7px;
	}
</style>
