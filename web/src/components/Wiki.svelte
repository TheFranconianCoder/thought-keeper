<script lang="ts">
	import Markdown from './Markdown.svelte';
	import Icon from './Icon.svelte';
	import CopyId from './CopyId.svelte';
	import { app } from '../lib/state.svelte';
	import { deleteWikiPage, loadWiki, saveWikiPage } from '../lib/actions';
	import { goto } from '../lib/nav';
	import { onMount } from 'svelte';

	let editing = $state(false);
	let preview = $state(false);
	let saving = $state(false);
	let title = $state('');
	let tags = $state('');
	let body = $state('');
	let filter = $state('');

	const selected = $derived(app.wiki.selected);
	const pages = $derived(
		filter
			? app.wiki.pages.filter((p) => [...(p.tags ?? []), ...(p.auto_tags ?? [])].includes(filter))
			: app.wiki.pages,
	);

	// Which page the editor was last synchronised with. A page REFRESH — a tagging run finishing, a
	// save from another window, the reconnect catch-up — replaces `app.wiki.selected` with an identical
	// page, and this effect used to read that assignment as "another page was opened": the editor closed
	// under the reader's hands and the detail view came back. It is bookkeeping, not state.
	let shownKey = '';

	$effect(() => {
		// The ROUTE owns which page is open. A new-page route starts an empty draft; opening a stored
		// page leaves edit mode; a refresh of the SAME page changes nothing at all.
		const draft = app.wiki.newPage;
		const key = draft ? 'new' : app.wiki.selectedId;
		if (key === shownKey) return;
		shownKey = key;
		if (draft) {
			editing = true;
			preview = false;
			title = '';
			tags = '';
			body = '';
			return;
		}
		editing = false;
		preview = false;
	});

	onMount(() => {
		if (!app.wiki.loaded) void loadWiki();
	});

	function startNew(): void {
		app.wiki.newPage = true;
		app.wiki.selected = null;
		app.wiki.selectedId = '';
		editing = true;
		preview = false;
		title = '';
		tags = '';
		body = '';
		goto('#/wiki/new');
	}

	function startEdit(): void {
		if (!selected) return;
		editing = true;
		preview = false;
		title = selected.title;
		tags = (selected.tags ?? []).join(', ');
		body = selected.body;
	}

	async function save(): Promise<void> {
		if (saving) return;
		saving = true;
		try {
			const saved = await saveWikiPage(selected?.id ?? '', {
				title,
				tags: tags
					.split(',')
					.map((t) => t.trim())
					.filter(Boolean),
				body,
			});
			if (saved) {
				editing = false;
				preview = false;
			}
		} finally {
			saving = false;
		}
	}

	function cancel(): void {
		editing = false;
		preview = false;
		if (app.wiki.newPage) {
			app.wiki.newPage = false;
			goto('#/wiki');
		}
	}

	function open(id: string): void {
		goto('#/wiki/' + id);
	}

	function chooseTag(tag: string): void {
		filter = filter === tag ? '' : tag;
		// The cloud is a lens over the LIST: choosing a tag while a page is open returns to the
		// pages that carry it instead of leaving the reader on a page the filter cannot show.
		if (app.wiki.selected) goto('#/wiki');
	}
</script>

<div class="wiki">
	<div class="page-head">
		<h1>Wiki</h1>
		<span class="spacer"></span>
		{#if !editing}
			<button class="btn btn-accent" onclick={startNew}><Icon name="plus" /> New page</button>
		{/if}
	</div>

	{#if app.wiki.notice}<p class="muted">{app.wiki.notice}</p>{/if}

	<div class="wiki-layout">
		<main class="wiki-main">
			{#if editing}
				<form class="wiki-editor" onsubmit={(e) => { e.preventDefault(); void save(); }}>
					<div class="wiki-editor-head">
						<div class="seg" role="tablist" aria-label="Wiki editor mode">
							<button type="button" class:on={!preview} onclick={() => (preview = false)}>Edit</button>
							<button type="button" class:on={preview} onclick={() => (preview = true)}>Preview</button>
						</div>
					</div>
					{#if preview}
						<div class="wiki-preview">
							<h2>{title.trim() || 'Untitled'}</h2>
							{#if tags.trim()}
								<div class="wiki-page-tags">
									{#each tags.split(',').map((t) => t.trim()).filter(Boolean) as t (t)}
										<span class="tag-chip on">{t}</span>
									{/each}
								</div>
							{/if}
							{#if body.trim()}
								<div class="markdown"><Markdown text={body} /></div>
							{:else}
								<p class="muted">Nothing to preview yet.</p>
							{/if}
						</div>
					{:else}
						<label class="field">
							<span>Title</span>
							<input class="input" bind:value={title} placeholder="Page title" autocomplete="off" />
						</label>
						<label class="field">
							<span>Tags <span class="muted">(comma-separated, frontmatter)</span></span>
							<input class="input" bind:value={tags} placeholder="go, concurrency, notes" autocomplete="off" />
						</label>
						<label class="field">
							<span>Markdown</span>
							<textarea class="input wiki-body" bind:value={body} spellcheck="true" placeholder="# A page&#10;&#10;Write Markdown here. Reference a wiki page with [[wiki:<id>]], a source with [[source:<hash>]]."></textarea>
						</label>
					{/if}
					<div class="wiki-editor-foot">
						<button class="btn btn-accent" type="submit" disabled={saving}>
							{saving ? 'Saving…' : 'Save page'}
						</button>
						<button class="btn" type="button" onclick={cancel}>Cancel</button>
						<span class="muted">Saving writes the page and reprocesses its source from capture.</span>
					</div>
				</form>
			{:else if selected}
				<article class="wiki-page">
					<header class="wiki-page-head">
						<div>
							<h2>{selected.title}</h2>
							<div class="wiki-id"><span class="muted">ID</span><CopyId text={selected.id} label="Wiki ID" /></div>
						</div>
						<div class="wiki-page-actions">
							{#if selected.source_hash}
								<a class="btn" href="#/sources/{selected.source_hash}" title="Open the source representation">
									<Icon name="db" /> Source
								</a>
							{/if}
							<button class="btn" onclick={startEdit}><Icon name="page" /> Edit</button>
							<button class="btn btn-danger" onclick={() => deleteWikiPage(selected.id)}>
								<Icon name="trash" /> Delete
							</button>
						</div>
					</header>
					{#if selected.tags?.length || selected.auto_tags?.length}
						<div class="wiki-page-tags">
							{#each selected.tags ?? [] as t (t)}
								<button class="tag-chip on" onclick={() => chooseTag(t)}>{t}</button>
							{/each}
							<!-- The derived tags sit beside the human's, marked: the reader can tell what the
							     app invented, and deleting one is respected by the next tagging run. -->
							{#each selected.auto_tags ?? [] as t (t)}
								<button
									class="tag-chip auto"
									title="derived from the page — the app tags it, you can still filter by it"
									onclick={() => chooseTag(t)}>{t}</button
								>
							{/each}
						</div>
					{/if}
					<!-- The links are IN the prose: a list under it was the same thing twice, and the
					     reader who wants a link clicks the text. -->
					<div class="markdown wiki-markdown"><Markdown text={selected.body} /></div>
				</article>
			{:else}
				{#if pages.length === 0}
					<div class="wiki-empty">
						<p>No wiki pages yet.</p>
						<button class="btn btn-accent" onclick={startNew}><Icon name="plus" /> Write the first page</button>
					</div>
				{:else}
					<div class="wiki-list">
						{#if filter}
							<p class="muted">Filtered by <span class="chip">{filter}</span>
								<button class="src-link" onclick={() => (filter = '')}>clear</button>
							</p>
						{/if}
						{#each pages as p (p.id)}
							<button class="wiki-row" onclick={() => open(p.id)}>
								<span class="wiki-row-title">{p.title || 'Untitled'}</span>
								{#if (p.tags?.length ?? 0) + (p.auto_tags?.length ?? 0) > 0}
									<span class="wiki-row-tags">
										{#each p.tags ?? [] as t (t)}<span class="tag">{t}</span>{/each}
										{#each p.auto_tags ?? [] as t (t)}<span class="tag auto" title="derived">{t}</span>{/each}
									</span>
								{/if}
								<span class="spacer"></span>
								<span class="muted">{p.updated_at?.slice(0, 10) ?? ''}</span>
							</button>
						{/each}
					</div>
				{/if}
			{/if}
		</main>

		<aside class="wiki-aside">
			<div class="rail-label">Tags</div>
			{#if app.wiki.tags.length === 0}
				<p class="muted">
					No tags yet. The app tags a page from its own text when you save it; you can add your own in
					the editor.
				</p>
			{:else}
				<div class="tag-cloud">
					{#each app.wiki.tags as t (t.tag)}
						<button class="tag-chip" class:on={filter === t.tag} onclick={() => chooseTag(t.tag)}>
							{t.tag}<span>{t.count}</span>
						</button>
					{/each}
				</div>
			{/if}
			<p class="muted wiki-hint">
				Reference pages by ID: <code>[[wiki:&lt;id&gt;]]</code><br />
				Reference sources by hash: <code>[[source:&lt;hash&gt;]]</code>
			</p>
		</aside>
	</div>
</div>
