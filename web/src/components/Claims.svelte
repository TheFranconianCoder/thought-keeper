<script lang="ts">
	import Icon from './Icon.svelte';
	import Markdown from './Markdown.svelte';
	import { app } from '../lib/state.svelte';
	import { goto } from '../lib/nav';
	import { openSourceAt } from '../lib/actions';
	import { api } from '../lib/api';
	import type { SearchHit } from '../lib/types';
	import { onMount } from 'svelte';

	// The Claims view is a bounded window, never the whole vault: the list is the newest X claims,
	// and a topic filter changes WHICH window is read. The topic cloud is the claim catalogue
	// (not pages), and it is bounded to its busiest names.
	const topics = $derived(app.claims.claimTopics);
	const selected = $derived(app.claims.selected);
	const claims = $derived(app.claims.items);
	const total = $derived(app.status?.index.atoms ?? app.claims.total);
	let query = $state('');
	let results = $state<SearchHit[]>([]);
	let searching = $state(false);
	let searchTimer: ReturnType<typeof setTimeout> | undefined;
	const searchMode = $derived(query.trim().length >= 2);

	async function runSearch(term: string): Promise<void> {
		searching = true;
		try {
			const res = await api.search(term, 'atoms', 50);
			if (query.trim() === term) results = res.hits ?? [];
		} catch {
			if (query.trim() === term) results = [];
		}
		if (query.trim() === term) searching = false;
	}

	function onSearch(e: Event): void {
		query = (e.currentTarget as HTMLInputElement).value;
		clearTimeout(searchTimer);
		const term = query.trim();
		if (term.length < 2) {
			results = [];
			searching = false;
			return;
		}
		searchTimer = setTimeout(() => void runSearch(term), 160);
	}

	// A [[Claim]] link in a body arrives as ?q=…: the finder is how a claim is looked up, because
	// the server resolves links by target and there is no claim-title route.
	onMount(() => {
		const q = new URLSearchParams(location.hash.split('?')[1] ?? '').get('q') ?? '';
		if (q.trim().length >= 2) {
			query = q;
			void runSearch(q.trim());
		}
	});

	function openClaim(id: string): void {
		goto('#/claims/' + id);
	}
	function filterTopic(topic: string): void {
		goto(topic ? '#/claims?topic=' + encodeURIComponent(topic) : '#/claims');
	}
</script>

{#if selected}
	<div class="doc-wrap">
		<article class="doc">
			<a class="back-link" href="#/claims">← All claims</a>
			<div class="claim-kicker">Claim</div>
			<h1 class="claim-statement">{selected.claim}</h1>
			<div class="doc-meta">
				{#if selected.topic}<a class="chip" href="#/claims?topic={encodeURIComponent(selected.topic)}">{selected.topic}</a>{/if}
				<span class="chip">{selected.type}</span>
				<span class="chip">{selected.status}</span>
				{#if selected.cluster_count}<span class="chip">{selected.cluster_count} cluster{selected.cluster_count === 1 ? '' : 's'}</span>{/if}
			</div>
			{#if selected.body}
				<div class="markdown"><Markdown text={selected.body} /></div>
			{/if}
		</article>

		<div class="page-foot">
			{#if selected.sources?.length}
				<div>
					<div class="foot-label">Sources<span class="foot-count">{selected.sources.length}</span></div>
					{#each selected.sources as source (source.hash + ':' + (source.node ?? ''))}
						<!-- The claim names the source NODE it came from, so the source page opens at
						     that part instead of its top. The node is a query parameter, so the URL
						     can be shared and back/forward return to it. -->
						<button class="foot-row" onclick={() => openSourceAt(source.hash, source.node)}>
							<Icon name="db" />
							<span class="t">{source.title || source.hash.slice(0, 16) + '…'}</span>
							<span class="s">node {source.node || '—'}{source.ocr ? ' · OCR' : ''}</span>
						</button>
					{/each}
				</div>
			{/if}

			{#if selected.cluster_count}
				<p class="muted">This claim travels in {selected.cluster_count} cluster{selected.cluster_count === 1 ? '' : 's'} — the association is not a place of its own, so the claims it travels with are listed below.</p>
			{/if}

			<div>
				<div class="foot-label">Related claims<span class="foot-count">{app.claims.related.length}</span></div>
				{#if selected.cluster_count}<p class="muted">Cluster peers come first; the rest share the topic.</p>{/if}
				{#if app.claims.related.length}
					{#each app.claims.related as related (related.id)}
						<a class="foot-row" href="#/claims/{related.id}">
							<Icon name="distill" />
							<span class="t">{related.claim}</span>
							<span class="s">{related.topic}</span>
						</a>
					{/each}
				{:else}
					<p class="muted">This claim has no cluster peers{selected.topic ? ` and no other claim in “${selected.topic}”` : ''} yet.</p>
				{/if}
			</div>

			{#if app.claims.notice}<p class="muted">{app.claims.notice}</p>{/if}
		</div>
	</div>
	{:else}
	<div class="home claims-home">
		<div class="page-head">
			<h1>Claims{app.claims.topic ? ` · ${app.claims.topic}` : ''}</h1>
			{#if app.claims.topic}<a class="chip" href="#/claims">clear topic</a>{/if}
		</div>
		<div class="claim-search">
			<input class="input" placeholder="Search claims…" value={query} oninput={onSearch} />
		</div>
		<p class="muted">
			{#if searchMode}
				Search results for “{query.trim()}”.
			{:else if app.claims.topic}
				Newest {Math.min(claims.length, 50)} claims in “{app.claims.topic}”.
			{:else}
				Newest {Math.min(claims.length, 50)} of {total} claims.
			{/if}
			{#if !searchMode}The list is deliberately bounded; use a topic or the search field above to narrow it.{/if}
		</p>

		{#if searchMode}
			{#if searching}
				<p class="muted">Searching claims…</p>
			{:else if results.length === 0}
				<p class="muted">No claim matches “{query.trim()}”.</p>
			{:else}
				<div class="list">
					{#each results as hit (hit.id)}
						<button class="claim-row" onclick={() => openClaim(hit.id)}>
							<Icon name="distill" />
							<span class="claim-row-main">
								<span class="t">{hit.title}</span>
								<span class="s">{hit.snippet}</span>
							</span>
						</button>
					{/each}
				</div>
			{/if}
		{:else if topics.length}
			<div class="rail-chips cloud">
				{#each topics as topic (topic.topic)}
					<a class="chip" class:chip-accent={app.claims.topic === topic.topic} href={topic.topic ? '#/claims?topic=' + encodeURIComponent(topic.topic) : '#/claims'}>
						{topic.topic}<span class="cloud-count">{topic.count}</span>
					</a>
				{/each}
			</div>
		{/if}

		{#if !searchMode}
		{#if app.claims.notice}
			<p class="muted">{app.claims.notice}</p>
		{:else if claims.length === 0}
			<div class="empty">
				<div class="big">{app.claims.loaded ? 'No claims here' : 'Nothing loaded'}</div>
				<p>{app.claims.loaded ? 'No claim matches this topic.' : 'Thought Keeper is not reachable.'}</p>
			</div>
		{:else}
			<div class="list">
				{#each claims as claim (claim.id)}
					<button class="claim-row" onclick={() => openClaim(claim.id)}>
						<Icon name="distill" />
						<span class="claim-row-main">
							<span class="t">{claim.claim}</span>
							<span class="s">{claim.topic} · {claim.type}{claim.cluster_count ? ` · ${claim.cluster_count} cluster${claim.cluster_count === 1 ? '' : 's'}` : ''}</span>
						</span>
					</button>
				{/each}
			</div>
		{/if}
		{/if}
	</div>
{/if}

<style>
	.claims-home { max-width: var(--doc-max); }
	.claim-search { margin: 0 0 12px; }
	.claim-search .input { width: 100%; }
	.back-link { display: inline-block; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
	.claim-kicker { margin: 8px 0 4px; color: var(--faint); font-size: 11px; font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase; }
	.claim-statement { margin: 0 0 14px; font-size: 22px; font-weight: 600; line-height: 1.4; letter-spacing: -0.01em; color: var(--text); }
	.claim-row { display: flex; align-items: center; gap: 9px; width: 100%; padding: 8px 9px; border: 0; border-bottom: 1px solid var(--line); background: transparent; color: inherit; text-align: left; cursor: pointer; font: inherit; }
	.claim-row:hover { background: var(--bg-hover); }
	.claim-row-main { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
	.claim-row-main .t { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.claim-row-main .s { color: var(--faint); font-size: 11.5px; }
	.cloud { margin: 16px 0 18px; }
	.cloud-count { margin-left: 6px; color: var(--faint); font-size: 10.5px; font-variant-numeric: tabular-nums; }
</style>
