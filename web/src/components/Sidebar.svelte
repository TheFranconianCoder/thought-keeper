<script lang="ts">
	import Icon from './Icon.svelte';
	import { app } from '../lib/state.svelte';
	import { goto } from '../lib/nav';
	import { openAdd, openSearch } from '../lib/actions';

	// The sidebar is a small set of FINITE lenses, not a catalogue of every topic. A topic is a
	// label on claims; it belongs in the claims view as a filter, not here as a navigation tree.
	const bookmarkCount = $derived(app.sources.items.filter((s) => s.bookmark).length);
	const claimCount = $derived(app.status?.index.atoms ?? 0);

	const dotClass = (c: string): string =>
		c === 'live' ? 'dot' : c === 'reconnecting' ? 'dot dot-warn' : 'dot dot-off';
</script>

<aside class="sidebar">
	<div class="brand">
		<img class="brand-mark" src="/icon.svg" alt="" width="22" height="22" />
		<span class="brand-name">Thought Keeper</span>
	</div>

	<div class="side-search" role="search" title="Search claims (Ctrl/Cmd+K)">
		<Icon name="search" />
		<input
			aria-label="Search claims"
			placeholder="Search claims…"
			autocomplete="off"
			spellcheck="false"
			onfocus={() => openSearch()}
			onclick={() => openSearch()}
		/>
	</div>

	<button class="nav-item {app.view === 'claims' ? 'active' : ''}" onclick={() => goto('#/claims')}>
		<Icon name="distill" /><span>Claims</span>
		{#if claimCount}<span class="count">{claimCount}</span>{/if}
	</button>
	<button class="nav-item {app.view === 'wiki' ? 'active' : ''}" onclick={() => goto('#/wiki')}>
		<Icon name="page" /><span>Wiki</span>
		{#if app.wiki.pages.length}<span class="count">{app.wiki.pages.length}</span>{/if}
	</button>
	<button class="nav-item {(app.view === 'sources' || app.view === 'source') ? 'active' : ''}" onclick={() => goto('#/sources')}>
		<Icon name="db" /><span>Sources</span>
		{#if app.status}<span class="count">{app.status.index.sources}</span>{/if}
	</button>
	<button class="nav-item {app.view === 'attention' ? 'active' : ''}" onclick={() => goto('#/attention')}>
		<Icon name="info" /><span>Attention</span>
	</button>
	<button class="nav-item {app.view === 'bookmarks' ? 'active' : ''}" onclick={() => goto('#/bookmarks')}>
		<Icon name="link" /><span>Bookmarks</span>
		{#if bookmarkCount}<span class="count">{bookmarkCount}</span>{/if}
	</button>
	<button class="nav-item {app.view === 'ask' ? 'active' : ''}" onclick={() => goto('#/ask')}>
		<Icon name="spark" /><span>Ask</span>
	</button>

	<div class="side-foot">
		<button class="nav-item" onclick={() => openAdd()}>
			<Icon name="plus" /><span>Add source</span>
		</button>
		<button class="nav-item {app.view === 'status' ? 'active' : ''}" onclick={() => goto('#/status')}>
			<Icon name="info" /><span>Status</span>
		</button>
		<span class="row"><span class={dotClass(app.connection)}></span>{app.connection}</span>
	</div>
</aside>
