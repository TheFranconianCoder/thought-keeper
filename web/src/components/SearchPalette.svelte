<script lang="ts">
	import Icon from './Icon.svelte';
	import { app } from '../lib/state.svelte';
	import { scheduleSearch, closeSearch } from '../lib/actions';
	import { goto } from '../lib/nav';

	let input = $state<HTMLInputElement | undefined>(undefined);

	$effect(() => {
		if (app.ui.search) input?.focus();
	});

	$effect(() => {
		if (!app.ui.search) return;
		const onKey = (e: KeyboardEvent): void => {
			if (e.key === 'Escape') close();
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});

	function close(): void {
		closeSearch();
		app.search.q = '';
		app.search.hits = [];
		app.search.active = 0;
		app.search.busy = false;
	}

	function pick(hit: { id: string }): void {
		close();
		goto('#/claims/' + hit.id);
	}

	function onKey(e: KeyboardEvent): void {
		if (e.key === 'Escape') {
			close();
		} else if (e.key === 'ArrowDown') {
			e.preventDefault();
			app.search.active = Math.min(app.search.active + 1, app.search.hits.length - 1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			app.search.active = Math.max(app.search.active - 1, 0);
		} else if (e.key === 'Enter') {
			const hit = app.search.hits[app.search.active];
			if (hit) pick(hit);
		}
	}
</script>

{#if app.ui.search}
	<div class="overlay">
		<button class="overlay-bg" aria-label="Close search" onclick={close}></button>
		<div class="palette" role="dialog" aria-label="Search claims" tabindex="-1">
			<input
				bind:this={input}
				placeholder="Search claims…"
				value={app.search.q}
				autocomplete="off"
				spellcheck="false"
				oninput={(e) => scheduleSearch(e.currentTarget.value)}
				onkeydown={onKey}
			/>
			<div class="results">
				{#if app.search.busy}
					<p class="muted" style="padding:12px 14px;margin:0">Searching claims…</p>
				{:else if app.search.hits.length === 0}
					<p class="muted" style="padding:12px 14px;margin:0">
						{app.search.q.trim().length < 2 ? 'Type to search claims.' : 'No matching claims.'}
					</p>
				{:else}
					{#each app.search.hits as h, i (h.id)}
						<button
							class="hit {i === app.search.active ? 'active' : ''}"
							onclick={() => pick(h)}
							onmouseenter={() => (app.search.active = i)}
						>
							<Icon name="distill" />
							<span>
								<span class="t">{h.title}</span><br />
								<span class="s">{h.snippet}</span>
							</span>
							<span class="kind">claim</span>
						</button>
					{/each}
				{/if}
			</div>
			<div class="hint">↑↓ navigate · ⏎ open · esc close</div>
		</div>
	</div>
{/if}
