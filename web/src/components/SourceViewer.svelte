<script lang="ts">
	import Icon from './Icon.svelte';
	import CopyId from './CopyId.svelte';
	import SourceSteps from './SourceSteps.svelte';
	import Markdown from './Markdown.svelte';
	import { app } from '../lib/state.svelte';
	import { api } from '../lib/api';
	import { capture, closeViewer, rebuildTree, distill, resetSource, setViewerState } from '../lib/actions';
	import { fmtBytes, relTime, splitFrontmatter } from '../lib/util';
	import { countsOf, isWorking, statusLabel } from '../lib/sourceStatus';
	import type { Atom, Source, SourceNode, SourceTopic } from '../lib/types';
	import { onMount } from 'svelte';

	let content = $state<string | null>(null);
	let atoms = $state<Atom[]>([]);
	// The claims list is BOUNDED (the server returns at most 100 of them): a book has hundreds, and
	// shipping every body is what made opening the overlay feel slow. The total and the chips come
	// with it, computed over ALL of the source's claims, so the rail still shows the whole picture.
	let atomTotal = $state(0);
	let atomTopics = $state<SourceTopic[]>([]);
	let meta = $state<Source | null>(null);
	// True while the content shown is the source's RENDERING (the text step 1 extracted): it is a
	// plain Markdown document, whatever the source's own extension is.
	let rendered = $state(false);
	// pdfReady delays the PDF frame by one paint, so "Loading the PDF…" is visible before the
	// browser's viewer takes over the window.
	let pdfReady = $state(false);
	// The file being looked at in the Files tab: the sidecar's name (or its object flag), and what it
	// contains. Every file of a source is readable — the step record, the outline, the log — because
	// "what has this source been through" should be answerable without leaving the app.
	// The open FILE is part of the address too (nav.ts): the name is what the URL carries, and the rest
	// (which sidecar, is it an object) is read back from the source's file list.
	const openFile = $derived.by(() => {
		const name = app.ui.viewerFile;
		if (!name) return null;
		const c = sidecars.find((x) => x.name === name);
		return { which: name, name, object: !!c?.object };
	});
	// The source's OUTLINE (step 2) and the part that is open. A document of megabytes is not
	// rendered at once: the tree IS its table of contents, and the reader walks it part by part
	// — the same substrate the agent retrieves with.
	let outline = $state<SourceNode[]>([]);
	const part = $derived.by(() => {
		const id = app.ui.viewerPart;
		if (!id) return null;
		// The TITLE comes from the outline, not from the id: the node is the address, the title is
		// what the document calls the part. Before the tree arrives the id is all there is.
		const node = outline.find((n) => n.node_id === id);
		return { id, title: node?.title || '' };
	});
	let partText = $state<string | null>(null);
	let partError = $state<string | null>(null);
	let fileText = $state<string | null>(null);
	let fileError = $state<string | null>(null);
	// The head carries only the TABS: what you are looking at (the content, the log, the files this
	// source consists of). The state and the numbers live in the rail, where the lights are.
	// The TAB, the open outline SECTION and the open FILE are part of the address (see nav.ts): each of
	// them is a step back/forward returns to, so they live in the store and the route sets them.
	type Tab = 'snapshot' | 'pdf' | 'live' | 'log' | 'files';
	const tab = $derived((app.ui.viewerTab || 'snapshot') as Tab);
	let atomsError = $state<string | null>(null);


	const viewer = $derived(app.ui.viewer);

	/** The document split into its YAML header and its body. The header is SHOWN (as monospace, above
	 *  the prose) rather than hidden: it is what the source declares about itself — a URL's address and
	 *  fetch time, a page's provenance. */
	const doc = $derived(splitFrontmatter(content ?? ''));
	const file = $derived(fileText === null ? { front: '', body: '' } : splitFrontmatter(fileText));

	const isMarkdown = (ext: string, kind: string): boolean =>
		ext === '.md' || ext === '.markdown' || kind === 'url';

	const sidecars = $derived(meta?.sidecars ?? []);
	// A source has NO topic of its own: its CLAIMS carry the flat topics, so the rail shows the ones
	// the claims actually have — with their counts, and only the head of the list. A source with a
	// hundred claims in twenty topics says what it is about in five chips, not twenty.
	const TOP_TOPICS = 5;
	const topics = $derived.by((): SourceTopic[] => {
		if (atomTopics.length) return atomTopics;
		const counts = new Map<string, number>();
		for (const a of atoms) {
			if (a.topic) counts.set(a.topic, (counts.get(a.topic) ?? 0) + 1);
		}
		return [...counts.entries()]
			.map(([name, count]) => ({ name, count }))
			.sort((x, y) => y.count - x.count || x.name.localeCompare(y.name));
	});
	const topTopics = $derived(topics.slice(0, TOP_TOPICS));
	

	/** How much of a document is rendered inline. `marked` + the sanitizer run SYNCHRONOUSLY on the
	 *  main thread, so a whole book in the DOM blocks the window — and while it does, the overlay
	 *  cannot paint, which is exactly what "the overlay takes seconds to open" was. 64 KiB is a real
	 *  preview (tens of pages) and renders in a blink; the raw file is one tab away. */
	const RENDER_CAP = 64 * 1024;
	const preview = $derived(content === null ? '' : content.slice(0, RENDER_CAP));
	const truncated = $derived(content !== null && content.length > RENDER_CAP);
	// A rendering is Markdown even when the source is a PDF.
	const markdownView = $derived(isMarkdown(viewer?.ext ?? '', viewer?.kind ?? '') || rendered);
	// A document above the render cap is not shown at once. Its OUTLINE is (the parts, one click
	// each) when step 2 has run; without a tree the preview stays capped and says why.
	const big = $derived(content !== null && content.length > RENDER_CAP);
	// The outline without its synthetic root; the nodes with text are the parts to read (one part is
	// one work unit of the derivation), the rest are the grouping headings that give it its shape.
	const parts = $derived(outline.filter((n) => n.depth > 0));
	const readable = $derived(parts.filter((n) => n.has_text));
	const partDoc = $derived(splitFrontmatter((partText ?? '').slice(0, RENDER_CAP)));
	const partTruncated = $derived((partText?.length ?? 0) > RENDER_CAP);

	/** load reads everything the overlay shows. `fresh` is true when a DIFFERENT source is being
	 *  opened: only then is the view cleared — an event while the overlay is open reloads the data and
	 *  leaves the tab, the scroll position and the visible content alone until the new one arrives.
	 *  Clearing on every event is exactly what a reader sees as flicker. */
	async function load(hash: string, fresh: boolean): Promise<void> {
		if (fresh) {
			content = null;
			atoms = [];
			atomsError = null;
			meta = null;
			outline = [];
			// NOT the tab, the part or the file: the ROUTE owns those (see nav.ts / App.svelte's
			// applyOverlay). Setting them here is what made a link to `?tab=log` open Snapshot, and
			// back/forward unable to return to a tab.
			partText = null;
			partError = null;
			app.ui.sourceLog = null;
		}
		const stillOpen = (): boolean => app.ui.viewer?.hash === hash;
		api.source(hash).then((s) => stillOpen() && (meta = s)).catch(() => stillOpen() && (meta = null));
		// The outline is titles and summaries only (the texts come per part), so it is cheap even
		// for a book — and it is what makes a large source readable at all.
		api
			.sourceTree(hash)
			.then((d) => {
				if (stillOpen()) outline = d.nodes ?? [];
			})
			.catch(() => stillOpen() && (outline = []));
		api
			.sourceLog(hash)
			.then((t) => stillOpen() && (app.ui.sourceLog = { hash, text: t }))
			.catch(() => stillOpen() && (app.ui.sourceLog = null));
		api
			.sourceAtoms(hash)
			.then((d) => {
				if (!stillOpen()) return;
				atoms = d.atoms ?? [];
				atomTotal = d.total ?? atoms.length;
				atomTopics = d.topics ?? [];
				
			})
			.catch((e) => {
				if (!stillOpen()) return;
				atoms = [];
				atomsError = e instanceof Error ? e.message : String(e);
			});
		const v = app.ui.viewer;
		if (v) {
			// A PDF's bytes are not readable: what the reader gets is the RENDERING step 1 wrote (the
			// text the pipeline itself read) — instant, instead of pulling a whole PDF into a frame
			// that takes seconds to paint. The PDF itself is one click away, in its own tab.
			const which = v.ext === '.pdf' ? ('render' as const) : undefined;
			rendered = which === 'render';
			api
				.rawText(hash, which)
				.then((t) => {
					if (!stillOpen()) return;
					const next = t;
					// Same bytes: keep the rendered DOM. Re-setting an identical string re-renders the
					// whole Markdown tree, and that flash is what a reader sees as flicker.
					if (next !== content) content = next;
				})
				.catch(() => {
					// A PDF whose capture has not produced text yet says so; anything else is an error.
					if (!stillOpen() || content !== null) return;
					content = which === 'render' ? '' : '[could not load this source]';
				});
		} else if (fresh) {
			content = null;
		}
	}

	// The Files tab always opens with its LIST. What you read there last is not what you came back
	// for, and a view that remembers it hides the list behind an extra click. Leaving the tab drops the
	// file (and its text, which can be megabytes).
	$effect(() => {
		if (tab !== 'files') {
			// Only the loaded text: the ADDRESS is the route's (setViewerState drops `file` when the tab
			// changes, which is where a navigation belongs). Writing the store here edited the URL's
			// meaning without a history entry.
			fileText = null;
			fileError = null;
		}
	});

	// The PDF frame mounts one paint AFTER its tab was chosen: the placeholder is what the reader
	// sees while the viewer loads, instead of an unchanged window.
	$effect(() => {
		const want = tab === 'pdf' && app.ui.viewer?.ext === '.pdf';
		if (!want) {
			pdfReady = false;
			return;
		}
		const raf = requestAnimationFrame(() => (pdfReady = true));
		return () => cancelAnimationFrame(raf);
	});

	// A part is part of the ADDRESS (`?part=<node>`), so it can arrive without a click: a shared
	// link, a reload, back/forward. The text is therefore loaded HERE, not in the click handler —
	// one path for a click, a deep link and a live update. The sourceVersion dependency keeps an
	// open part current when the source is derived again.
	let partSeq = 0;
	let loadedPartKey = '';
	$effect(() => {
		const hash = app.ui.viewer?.hash ?? '';
		const id = app.ui.viewerPart;
		void app.ui.sourceVersion;
		const seq = ++partSeq;
		if (!hash || !id) {
			partText = null;
			partError = null;
			loadedPartKey = '';
			return;
		}
		const key = hash + '|' + id;
		if (key !== loadedPartKey) {
			// A DIFFERENT part: the old text must not stand in while the new one loads. A version
			// bump for the SAME part keeps it, so a background event does not flash "Loading…".
			partText = null;
			partError = null;
		}
		loadedPartKey = key;
		const stillOpen = (): boolean =>
			seq === partSeq && app.ui.viewer?.hash === hash && app.ui.viewerPart === id;
		void api
			.sourceNode(hash, id)
			.then((n) => {
				if (!stillOpen()) return;
				partText = n.text ?? '';
				partError = null;
			})
			.catch((e) => {
				if (!stillOpen()) return;
				// A node that a tree rebuild removed: say so and offer the whole source, instead of
				// showing "Loading…" forever.
				partText = '';
				partError = e instanceof Error ? e.message : String(e);
			});
	});

	/** openPart puts ONE part into the address; the effect above loads its text. */
	function openPart(id: string): void {
		setViewerState({ part: id });
	}

	/** prettyJson indents a file that IS JSON (the step record, the outline): a wall of one-line JSON
	 *  is a file you cannot read, and these two are meant to be read.
	 *
	 *  BOUNDED: parsing and re-serialising happens on the main thread, and an outline carries every
	 *  node's text — a book's is megabytes. Above the cap the file is shown as it is (and the viewer
	 *  caps that too), because a pretty-print that hangs the window is worse than one long line. */
	const PRETTY_CAP = 512 * 1024;
	function prettyJson(text: string): string {
		if (text.length > PRETTY_CAP) return text;
		const trimmed = text.trim();
		if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return text;
		try {
			return JSON.stringify(JSON.parse(trimmed), null, 2) + '\n';
		} catch {
			return text;
		}
	}

	/** viewFile loads one file of the source into the Files tab's viewer. A PDF is not text: it is
	 *  offered as the PDF it is, in its own tab. */
	async function viewFile(which: string, name: string, object: boolean, ext: string): Promise<void> {
		setViewerState({ file: name });
		fileError = null;
		fileText = null;
		if (object && ext === '.pdf') return; // binary: the PDF tab is the way to look at it
		try {
			const t = await api.rawText(viewer?.hash ?? '', object ? 'object' : which);
			if (openFile?.name === name) fileText = prettyJson(t);
		} catch (e) {
			if (openFile?.name === name) fileError = e instanceof Error ? e.message : String(e);
		}
	}
	// `sourceVersion` is bumped by the event pipeline; reading it here is what keeps the overlay live
	// without polling.
	let lastHash: string | null = null;
	$effect(() => {
		const v = app.ui.viewer;
		const version = app.ui.sourceVersion;
		void version;
		if (!v) {
			lastHash = null;
			return;
		}
		const fresh = lastHash !== v.hash;
		lastHash = v.hash;
		void load(v.hash, fresh);
	});

	onMount(() => {
		const onKey = (e: KeyboardEvent): void => {
			if (e.key === 'Escape') closeViewer();
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});
</script>

{#if viewer}
	<div class="source-page">
		<div class="viewer" role="region" aria-label="Source">
			<div class="viewer-head">
				<span class="t">{viewer.title}</span>
				<span class="chip">{viewer.kind}</span>
				<span class="spacer"></span>
				<!-- Only the tabs: the state of this source (its four steps, its numbers, its files)
				     is in the rail beside the content. -->
				<span class="seg">
					{#if viewer.kind === 'url'}
						<button class:on={tab === 'snapshot'} onclick={() => setViewerState({ tab: 'snapshot' })}>Snapshot</button>
						<button class:on={tab === 'live'} onclick={() => setViewerState({ tab: 'live' })}>Live</button>
					{:else}
						<button class:on={tab === 'snapshot'} onclick={() => setViewerState({ tab: 'snapshot' })}>
							{viewer.ext === '.pdf' ? 'Text' : 'Content'}
						</button>
						{#if viewer.ext === '.pdf'}
							<!-- The PDF itself loads only when it is asked for: pulling a whole document
							     into a frame is never instant, and the text is what the pipeline read. -->
							<button class:on={tab === 'pdf'} onclick={() => setViewerState({ tab: 'pdf' })}>PDF</button>
						{/if}
					{/if}
					<button class:on={tab === 'log'} onclick={() => setViewerState({ tab: 'log' })}>Log</button>
					<button class:on={tab === 'files'} onclick={() => setViewerState({ tab: 'files' })}>Files</button>
				</span>
				<button class="icon-btn" title="Back to sources" onclick={closeViewer}><Icon name="back" /></button>
			</div>

			<div class="viewer-cols">
				<div class="viewer-main">
					{#if tab === 'files'}
						{#if openFile}
							<!-- One file, as it is on disk: monospace, and a YAML header in its own block so it
							     reads as metadata instead of as prose. -->
							<div class="file-view">
								<div class="file-head">
									<button class="icon-btn" title="Back to the files" onclick={() => setViewerState({ file: '' })}>
										<Icon name="chevron" />
									</button>
									<code>{openFile.name}</code>
									{#if openFile.object}<span class="chip">object</span>{/if}
									<span class="spacer"></span>
									{#if viewer.ext === '.pdf' && openFile.object}
										<button class="btn" onclick={() => setViewerState({ tab: 'pdf' })}>Open the PDF</button>
									{/if}
								</div>
								{#if fileError}
									<p class="muted" style="padding:0 22px">{fileError}</p>
								{:else if viewer.ext === '.pdf' && openFile.object}
									<p class="muted" style="padding:0 22px">
										A PDF is not text — it is shown as what it is, in its own tab.
									</p>
								{:else if fileText === null}
									<p class="muted" style="padding:22px">Loading…</p>
								{:else}
									{#if (fileText?.length ?? 0) > RENDER_CAP}
										<p class="muted viewer-cap">
											Showing the first {fmtBytes(RENDER_CAP)} of {fmtBytes(fileText?.length ?? 0)}.
										</p>
									{/if}
									{#if file.front}
										<pre class="file-front" title="frontmatter">{file.front}</pre>
									{/if}
									<pre class="file-body">{fileText?.slice(0, RENDER_CAP) ?? ''}</pre>
								{/if}
							</div>
						{:else}
							<div class="sidecar-table">
								<p class="muted" style="padding:0 22px">
									Everything about this source is one folder entry: the object and the files derived from
									it. Pick one to read it. A size of <b>—</b> means the file is missing — the consistency
									job notices that and derives the source again.
								</p>
								<table>
									<thead><tr><th>file</th><th>size</th></tr></thead>
									<tbody>
										{#each sidecars as c (c.name)}
											<tr class:missing={c.bytes < 0}>
												<td>
													{#if c.bytes < 0}
														<code>{c.name}</code>
													{:else}
														<button class="file-link" onclick={() => viewFile(c.name, c.name, !!c.object, viewer.ext)}>
															<code>{c.name}</code>
														</button>
													{/if}
													{#if c.object} <span class="chip">object</span>{/if}
												</td>
												<td>{c.bytes < 0 ? '—' : fmtBytes(c.bytes)}</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/if}
					{:else if tab === 'log'}
						<!-- Capped like every other long text: the log is bounded at 1 MiB by the server,
						     and a <pre> that size is a window that stops answering. The newest lines are
						     the interesting ones, so the TAIL is what is shown. -->
						{@const logText = app.ui.sourceLog?.hash === viewer.hash ? app.ui.sourceLog.text : ''}
						{#if logText.length > RENDER_CAP}
							<p class="muted viewer-cap">
								Showing the last {fmtBytes(RENDER_CAP)} of {fmtBytes(logText.length)}.
							</p>
						{/if}
						<pre class="srclog">{logText
								? logText.slice(-RENDER_CAP)
								: 'no log entries'}</pre
						>
					{:else if tab === 'live' && meta?.url}
						<iframe class="pdf" title="Live page" src={meta.url}></iframe>
					{:else if tab === 'pdf' && viewer.ext === '.pdf'}
						<!-- The PDF is loaded only now, and the placeholder gets its own paint first: a
						     browser's PDF viewer takes a moment with a large document, and a window that
						     shows nothing while it does looks like it hung. -->
						{#if pdfReady}
							<iframe class="pdf" title={viewer.title} src={api.rawURL(viewer.hash)}></iframe>
						{:else}
							<p class="muted" style="padding:22px">Loading the PDF…</p>
						{/if}
					{:else if part}
						<!-- ONE part, read on its own: this is how a document of megabytes is read. -->
						<div class="file-head">
							<button class="icon-btn" title="Back to the outline" onclick={() => setViewerState({ part: '' })}>
								<Icon name="chevron" />
							</button>
							<code>{part.id}</code>
							<span>{part.title}</span>
						</div>
						{#if partError}
							<p class="muted" style="padding:22px">
								This part is no longer in the source's outline: {partError}
								<button class="btn" onclick={() => setViewerState({ part: '' })}>Show the whole source</button>
							</p>
						{:else if partText === null}
							<p class="muted" style="padding:22px">Loading…</p>
						{:else}
							{#if partTruncated}
								<p class="muted viewer-cap">
									This part alone is {fmtBytes(partText?.length ?? 0)} — showing the first
									{fmtBytes(RENDER_CAP)}.
								</p>
							{/if}
							{#if partDoc.front}
								<pre class="file-front" title="frontmatter">{partDoc.front}</pre>
							{/if}
							{#if markdownView}
								<div class="markdown viewer-md"><Markdown text={partDoc.body} /></div>
							{:else}
								<pre class="raw">{partDoc.body}</pre>
							{/if}
						{/if}
					{:else if content && big && readable.length > 0}
						<!-- Too big to render at once — so it is not rendered at once. The outline is the
						     document's own table of contents (step 2), part by part. -->
						<div class="outline">
							<p class="muted viewer-cap">
								{fmtBytes(content.length)} — too much for one page. Its outline, {readable.length}
								readable parts; pick one.
							</p>
							{#each parts as n (n.node_id)}
								{#if n.has_text}
									<button
										class="outline-row"
										style="padding-left:{10 + (n.depth - 1) * 14}px"
										title={n.summary || n.title}
										onclick={() => openPart(n.node_id)}
									>
										<span class="o-id">{n.node_id}</span>
										<span class="o-t">{n.title || '(untitled)'}</span>
										{#if n.ocr}<span class="chip">ocr</span>{/if}
									</button>
								{:else}
									<div class="outline-group" style="padding-left:{10 + (n.depth - 1) * 14}px">
										<span class="o-id">{n.node_id}</span>
										<span class="o-t">{n.title || '(untitled)'}</span>
									</div>
								{/if}
							{/each}
						</div>
					{:else if content === null}
						<p class="muted" style="padding:22px">Loading…</p>
					{:else if content === '' && viewer.ext === '.pdf'}
						<p class="muted" style="padding:22px">
							No readable text yet — this source has not been captured, or the extraction produced
							nothing. <button class="btn" onclick={() => setViewerState({ tab: 'pdf' })}>Open the PDF</button>
						</p>
					{:else}
						{#if truncated}
							<p class="muted viewer-cap">
								Showing the first {fmtBytes(RENDER_CAP)} of {fmtBytes(content.length)} — this source has
								no outline yet, so there is nothing to walk. Build the outline (the rail's outline action)
								and it becomes readable part by part.
							</p>
						{/if}
						{#if markdownView}
							<!-- The declared header first, as monospace: it belongs to the document (a URL's
							     address and fetch time), and hiding it hides the provenance. -->
							{#if doc.front}
								<pre class="file-front" title="frontmatter">{doc.front}</pre>
							{/if}
							<div class="markdown viewer-md"><Markdown text={doc.body} /></div>
						{:else}
							<pre class="raw">{preview}</pre>
						{/if}
					{/if}
				</div>

				<aside class="viewer-rail">
					{#if meta}
						{@const hash = meta.hash}
						<!-- The state of this source in one block: the four lights with the two progress
						     bars beside them (parts and claims — stacked, and gone once the source is
						     finished) and, below, what the source is made of. The LIGHT says what is
						     running, done, failed or stale; the BAR says how far it has come; the COUNT
						     says how much there is. -->
						<div class="rail-part">
							<div class="rail-label">Steps</div>
							{#if !meta.bookmark}
								<SourceSteps source={meta} wide />
							{/if}
							<div class="rail-actions">
								<button class="icon-btn" title="Capture — read the source again (extract, render, name)" onclick={() => capture(hash)}>
									<Icon name="capture" />
								</button>
								<button class="icon-btn" title="Outline — rebuild the structure from the capture" onclick={() => rebuildTree(hash)}>
									<Icon name="tree" />
								</button>
								<button class="icon-btn" title="Distill — write the claims of this source (continues where it stopped)" onclick={() => distill(hash)}>
									<Icon name="distill" />
								</button>
								<button class="icon-btn" title="Reset — throw the outline and the claims away and derive again" onclick={() => resetSource(hash)}>
									<Icon name="rewind" />
								</button>
							</div>
						</div>
					{/if}

					{#if meta}
						<div class="rail-part">
							<div class="rail-label">Details</div>
							<div class="kv"><span class="k">kind</span><span class="v">{meta.kind}</span></div>
							<div class="kv"><span class="k">size</span><span class="v">{fmtBytes(meta.bytes)}</span></div>
							{#if meta.language}<div class="kv"><span class="k">language</span><span class="v">{meta.language}</span></div>{/if}
							{#if meta.pages}<div class="kv"><span class="k">pages</span><span class="v">{meta.pages}</span></div>{/if}
							<div class="kv"><span class="k">outline</span><span class="v">{meta.tree_state}</span></div>
							<div class="kv"><span class="k">added</span><span class="v">{relTime(meta.imported_at)}</span></div>
							{#if meta.url_status}
								<div class="kv"><span class="k">http</span><span class="v">{meta.url_status} · {relTime(meta.url_checked_at)}</span></div>
							{/if}
							<div class="kv"><span class="k">ID</span><span class="v"><CopyId text={meta.hash} label="Source ID" /></span></div>
							{#if meta.original_name?.startsWith('wiki:')}
								<div class="kv"><span class="k">wiki</span><span class="v"><a class="src-link" href="#/wiki/{meta.original_name.slice('wiki:'.length)}">open the page</a></span></div>
							{:else if meta.origin === 'wiki' && meta.wiki_page}
								<div class="kv"><span class="k">wiki</span><span class="v"><a class="src-link" href="#/wiki/{meta.wiki_page}">linked from a page</a></span></div>
							{/if}
						</div>
					{/if}

					{#if topics.length}
						<div class="rail-part">
							<div class="rail-label">Topics ({topics.length})</div>
							<div class="rail-chips">
								{#each topTopics as t (t.name)}
									<a
										class="chip"
										title="{t.count} claim{t.count === 1 ? '' : 's'} on this source"
										href="#/claims?topic={encodeURIComponent(t.name)}"
										>{t.name}<span class="chip-n">{t.count}</span></a
									>
								{/each}
							</div>
							{#if topics.length > topTopics.length}
								<p class="muted" style="font-size:12px;margin:6px 0 0">
									+{topics.length - topTopics.length} more topic(s)
								</p>
							{/if}
						</div>
					{/if}


					{#if meta}
						{@const c = countsOf(meta)}
						<div class="rail-part">
							<!-- What the lights and the bars cannot say in words: the state, derived from the
							     same counts, and — while a run is in flight — the phase it is in right now.
							     The numbers themselves are the bars and the counts under the lights; saying
							     them again here would be the same number twice. -->
							<div class="rail-label">Progress</div>
							<div class="kv">
								<span class="k">state</span>
								<span class="v" class:working={isWorking(meta)}>
									{#if isWorking(meta)}<span class="spin"></span>{/if}
									{statusLabel(meta)}
								</span>
							</div>
							{#if isWorking(meta) && c.msg}
								<div class="kv"><span class="k">now</span><span class="v">{c.phase}: {c.msg}</span></div>
							{/if}
						</div>
					{/if}

					<div class="rail-part">
						<div class="rail-label">Claims ({atomTotal})</div>
						{#if atomsError}
							<p class="muted" style="font-size:12.5px">Could not load claims: {atomsError}</p>
						{:else if atoms.length === 0 && (meta?.provenance_rows ?? 0) > 0}
							<p class="muted" style="font-size:12.5px">
								{meta?.provenance_rows} provenance row(s) have no atom — the index is damaged
								(orphaned rows). Run <b>Reindex</b> to prune them, then derive the source again.
							</p>
						{:else if atoms.length === 0}
							<p class="muted" style="font-size:12.5px">No claims yet — this source has not been distilled.</p>
						{:else}
							{#each atoms as a (a.id)}
								<a class="claim" href="#/claims/{a.id}">
									<span class="claim-t">{a.claim}</span>
									<span class="claim-m">{a.topic}{a.status !== 'active' ? ` · ${a.status}` : ''}</span>
								</a>
							{/each}
							{#if atomTotal > atoms.length}
								<p class="muted" style="font-size:12.5px">
									Showing the first {atoms.length} of {atomTotal} claims.
								</p>
							{/if}
						{/if}
					</div>
				</aside>
			</div>
		</div>
	</div>
{/if}
