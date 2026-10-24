<script lang="ts">
	import Icon from './Icon.svelte';
	import SourceIcon from './SourceIcon.svelte';
	import SourceSteps from './SourceSteps.svelte';
	import { app } from '../lib/state.svelte';
	import type { Source } from '../lib/types';
	import {
	deleteSource,
	distill,
	openSource,
	rebuildTree,
	resetSource,
	openAdd,
} from '../lib/actions';
	import { api } from '../lib/api';
	import { fmtBytes, relTime } from '../lib/util';
	import { countLabel, statusLabel } from '../lib/sourceStatus';



	const open = (s: Source): void =>
		openSource({ hash: s.hash, title: s.title || s.hash.slice(0, 12), kind: s.kind, ext: s.ext });

	/** act runs a row action without leaving its button focused.
	 *
	 *  The actions are only visible while the row is hovered or keyboard-focused, and a MOUSE click
	 *  focuses the button — so after the pointer left, a focused (and therefore invisible) button would
	 *  stay behind, and pressing Enter would fire an action nobody can see. A keyboard activation
	 *  (`detail === 0`) keeps its focus, so tabbing through the actions still works. */
	const act = (e: MouseEvent, fn: () => void): void => {
		e.stopPropagation();
		fn();
		if (e.detail > 0) (e.currentTarget as HTMLElement | null)?.blur();
	};

	/** subLine is the second line: what the source WAS (the raw input: its file name or its URL), then
	 *  what it is. The title above belongs to the document (step 1 names it), so the raw input is shown
	 *  additionally — never instead. */
	const subLine = (s: Source): { raw: string; rest: string } => ({
		raw: s.original_name || s.url || s.hash.slice(0, 12),
		rest: `${s.kind} · ${fmtBytes(s.bytes)} · ${relTime(s.imported_at)}`,
	});
</script>

<div class="sources">
	<div class="page-head">
		<h1>Sources</h1>
		<span class="spacer"></span>
		<button class="btn btn-accent" onclick={() => openAdd()}><Icon name="plus" /> Add</button>
	</div>

	{#if app.sources.items.length === 0 && app.connection !== 'live'}
		<!-- The list was never READ (see Home): "No sources yet" is a claim, and offline it is not true. -->
		<div class="empty">
			<div class="big">Nothing loaded</div>
			<p>Thought Keeper is not reachable, so this is not the store's answer.<br />The connection
				notice above says what to do.</p>
		</div>
	{:else if app.sources.items.length === 0}
		<div class="empty">
			<div class="big">No sources yet</div>
			<p>Add a file or a URL. Every source stays as a local, immutable snapshot.</p>
		</div>
	{:else}
		<div class="list">
			{#each app.sources.items as s (s.hash)}
				{@const sub = subLine(s)}
				<div
					class="row-src"
					role="button"
					tabindex="0"
					title="{statusLabel(s)}{countLabel(s) ? ' · ' + countLabel(s) : ''}"
					onclick={() => open(s)}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), open(s))}
				>
					<!-- The source's own icon: a URL the site's favicon, everything else its KIND. -->
					<SourceIcon kind={s.kind} hash={s.hash} />
					<div class="main-col">
						<div class="t">
							{s.title || s.hash.slice(0, 12)}
							{#if s.bookmark}<span class="tag" title="collected link, not indexed">link</span>{/if}
							{#if s.original_name?.startsWith('wiki:')}<span class="tag" title="user-authored wiki page">wiki</span>{/if}
							{#if s.origin === 'wiki'}<span class="tag" title="linked from a wiki page">wiki URL</span>{/if}
						</div>
						<div class="sub">
							<span class="meta-line">{sub.rest}</span>
							<!-- The raw input at the END of the line: the title above belongs to the document,
							     so the file name / URL is additional information, not the label. -->
							<span class="orig">{sub.raw}</span>
						</div>
					</div>

					<!-- The state of the source, flush right: its four lights with the progress bars beside
					     them and, below, what the source is made of. The actions appear on hover and cover
					     the block, so a calm list stays calm and the row's state is what you see. -->
					<div class="end-col">
						{#if !s.bookmark}<SourceSteps source={s} />{/if}
						<div class="acts">
							<button
								class="icon-btn"
								title="Outline: rebuild this source's outline from the capture"
								onclick={(e) => act(e, () => rebuildTree(s.hash))}><Icon name="tree" /></button
							>
							<button
								class="icon-btn"
								title="Distill — write the claims of this source (continues where it stopped)"
								onclick={(e) => act(e, () => distill(s.hash))}><Icon name="distill" /></button
							>
							<button
								class="icon-btn"
								title="Reset: throw the outline and the claims away and derive them again (pages are kept)"
								onclick={(e) => act(e, () => resetSource(s.hash))}><Icon name="rewind" /></button
							>
							<button
								class="icon-btn"
								title="Delete (the snapshot moves to the trash; its sidecars go with it)"
								onclick={(e) => act(e, () => deleteSource(s.hash))}><Icon name="trash" /></button
							>
						</div>
					</div>
				</div>
			{/each}
		</div>
	{/if}
	<p class="muted">{app.sources.notice}</p>
</div>
