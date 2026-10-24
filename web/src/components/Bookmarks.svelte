<script lang="ts">
	import Icon from './Icon.svelte';
	import SourceIcon from './SourceIcon.svelte';
	import { app } from '../lib/state.svelte';
	import type { Source } from '../lib/types';
	import {
	openSource,
	openAdd,
} from '../lib/actions';
	import { api } from '../lib/api';
	import { relTime } from '../lib/util';

	let filter = $state('');

	const bookmarks = $derived(app.sources.items.filter((s) => s.bookmark));
	const grouped = $derived.by(() => {
		const f = filter.trim().toLowerCase();
		const byHost = new Map<string, Source[]>();
		for (const s of bookmarks) {
			if (f && !s.title.toLowerCase().includes(f) && !(s.url ?? '').toLowerCase().includes(f)) continue;
			let host = 'other';
			try {
				host = new URL(s.url ?? '').host.replace(/^www\./, '');
			} catch {
				/* keep 'other' */
			}
			const list = byHost.get(host) ?? [];
			list.push(s);
			byHost.set(host, list);
		}
		return [...byHost.entries()]
			.map(([host, items]) => ({ host, items }))
			.sort((a, b) => a.host.localeCompare(b.host));
	});

	const open = (s: Source): void =>
		openSource({ hash: s.hash, title: s.title || s.hash.slice(0, 12), kind: s.kind, ext: s.ext });
</script>

<div class="sources">
	<div class="page-head">
		<h1>Bookmarks</h1>
		<span class="pill pill-muted">{bookmarks.length}</span>
		<span class="spacer"></span>
		<button class="btn btn-accent" onclick={() => openAdd()}><Icon name="plus" /> Add</button>
	</div>

	{#if bookmarks.length === 0 && app.connection !== 'live'}
		<!-- Nothing was READ (see Home): offline this is not the store's answer. -->
		<div class="empty">
			<div class="big">Nothing loaded</div>
			<p>Thought Keeper is not reachable, so this is not the store's answer.<br />The connection
				notice above says what to do.</p>
		</div>
	{:else if bookmarks.length === 0}
		<div class="empty">
			<div class="big">No bookmarks yet</div>
			<p>Paste any link — pages that cannot be read (video, paywall, app) are collected here.</p>
		</div>
	{:else}
		<input class="input" placeholder="Filter bookmarks…" bind:value={filter} style="margin-bottom:14px" />
		{#each grouped as g (g.host)}
			<div class="topic-block">
				<h2>{g.host} <span class="muted">{g.items.length}</span></h2>
				{#each g.items as s (s.hash)}
					<button class="row-src" onclick={() => open(s)}>
						<!-- The same icon rule as everywhere: a URL the site's favicon (a link glyph when
						     the site has none). -->
						<SourceIcon kind={s.kind} hash={s.hash} />
						<div class="main-col">
							<div class="t">{s.title || s.url}</div>
							<div class="sub">
								{s.url}
								{#if s.url_checked_at}· checked {relTime(s.url_checked_at)}{/if}
							</div>
						</div>
						{#if s.url_status}
							<span class="meta">{s.url_status}</span>
						{/if}
					</button>
				{/each}
			</div>
		{/each}
	{/if}
</div>
