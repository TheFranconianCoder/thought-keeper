<script lang="ts">
	import Markdown from './Markdown.svelte';
	import Icon from './Icon.svelte';
	import { app } from '../lib/state.svelte';
	import { askQuery, deleteAsk } from '../lib/actions';
	import { api } from '../lib/api';
	import { goto } from '../lib/nav';
	import { relTime } from '../lib/util';
	import { onMount } from 'svelte';

	let question = $state('');
	let input = $state<HTMLInputElement | undefined>(undefined);
	let topics = $state<string[]>([]);

	onMount(() => {
		void api
			.claimTopics()
			.then((res) => {
				topics = (res.topics ?? []).map((t) => t.topic);
			})
			.catch(() => {});
	});

	const thinking = $derived(app.ask.busy);
	const selected = $derived(app.asks.selected);

	$effect(() => {
		input?.focus();
	});

	function submit(e: SubmitEvent): void {
		e.preventDefault();
		const q = question.trim();
		if (!q) return;
		// The question lives on the run (and is shown in the thinking state); the input itself is
		// cleared immediately, so the next question starts from an empty field.
		question = '';
		void askQuery(q);
	}

	function openSaved(id: string): void {
		goto('#/ask/' + id);
	}
</script>

<div class="ask">
	<div class="page-head"><h1>Ask</h1></div>

	<form class="row-inline" onsubmit={submit} style="margin-bottom:24px">
		<input class="input" bind:this={input} bind:value={question} autocomplete="off" placeholder="Ask the claims…" />
		<button class="btn btn-accent" type="submit">Ask</button>
	</form>

	{#if thinking}
		<!-- The waiting state spans the full width of the question row. It stays here until the run
		     has actually produced a result. -->
		<div class="ask-thinking">
			<span class="spinner"></span>
			<div>
				<div class="t">Searching the claims…</div>
				{#if app.ask.question}<div class="q">{app.ask.question}</div>{/if}
			</div>
		</div>
	{:else if selected}
		<div class="doc-wrap">
			<article class="doc">
				<a class="back-link" href="#/ask">← Saved asks</a>
				<h1 class="doc-title">{selected.question}</h1>
				<div class="doc-meta">
					<span class="chip">{relTime(selected.updated_at)}</span>
					<button class="chip chip-danger" onclick={() => deleteAsk(selected.id)}>
						<Icon name="trash" size={12} /> Delete
					</button>
				</div>
				<div class="markdown"><Markdown text={selected.answer} {topics} /></div>
			</article>
		</div>
	{:else}
		{#if app.asks.items.length}
			<div class="saved-asks">
				<div class="foot-label">Saved asks<span class="foot-count">{app.asks.items.length}</span></div>
				{#each app.asks.items as ask (ask.id)}
					<button class="foot-row saved-ask-row" onclick={() => openSaved(ask.id)}>
						<Icon name="spark" />
						<span class="t">{ask.question}</span>
						<span class="s">{relTime(ask.updated_at)}</span>
					</button>
				{/each}
			</div>
		{:else}
			<div class="empty">
				<div class="big">{app.asks.loaded ? 'No saved asks yet' : 'Nothing loaded'}</div>
				<p>{app.asks.loaded ? 'Ask a question — every answer is saved automatically.' : 'Thought Keeper is not reachable.'}</p>
			</div>
		{/if}
	{/if}

	{#if app.ask.notice && !thinking && !selected}<p class="muted">{app.ask.notice}</p>{/if}
	{#if app.asks.notice}<p class="muted">{app.asks.notice}</p>{/if}
</div>

<style>
	.saved-asks { max-width: var(--doc-max); margin: 0 auto 26px; }
	.saved-ask-row { align-items: center; }
	.saved-ask-row :global(.ico) { align-self: center; }
	.back-link { display: inline-block; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
	.chip-danger { border: 0; cursor: pointer; color: var(--danger, #b4241f); background: var(--danger-soft, #f8e6e4); }
</style>
