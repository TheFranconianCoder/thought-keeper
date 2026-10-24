<script lang="ts">
	import Icon from './Icon.svelte';
	import SourceIcon from './SourceIcon.svelte';
	import { app } from '../lib/state.svelte';
	import { openSource, openSourceByHash } from '../lib/actions';
	import { statusLabel } from '../lib/sourceStatus';
	import type { Finding } from '../lib/types';

	// Attention is a lens over the state that already exists: failed source steps and lint findings. It
	// is not a second health model — each row opens the thing it names.
	const failedSources = $derived(
		app.sources.items.filter((s) =>
			Object.values(s.steps ?? {}).some((st) => st.state === 'failed'),
		),
	);
	const findings = $derived(app.lint.findings.filter((f) => f.severity !== 'info'));
	const total = $derived(failedSources.length + findings.length);

	function openFindingSource(f: Finding): void {
		if (f.subject_kind === 'source' && f.subject_id) void openSourceByHash(f.subject_id);
	}
</script>

<div class="home attention">
	<div class="home-hero">
		<h1>Attention</h1>
		<p>{total} thing{total === 1 ? '' : 's'} need a decision or a repair.</p>
	</div>

	{#if failedSources.length}
		<div class="topic-block">
			<h2>Sources with failed steps<span class="muted">{failedSources.length}</span></h2>
			{#each failedSources as s (s.hash)}
				<button class="page-link" onclick={() => openSource({ hash: s.hash, title: s.title || s.hash.slice(0, 12), kind: s.kind, ext: s.ext })}>
					<SourceIcon kind={s.kind} hash={s.hash} />
					<span class="t">{s.title || s.hash.slice(0, 12)}</span>
					<span class="k">{statusLabel(s)}</span>
				</button>
			{/each}
		</div>
	{/if}

	{#if findings.length}
		<div class="topic-block">
			<h2>Lint findings<span class="muted">{findings.length}</span></h2>
			{#each findings as f (f.id)}
				<div class="attention-finding">
					<span class="chip {f.severity === 'error' ? 'chip-dirty' : ''}">{f.severity}</span>
					<span class="attention-msg">{f.message}</span>
					{#if f.subject_kind === 'atom' && f.subject_id}
						<a class="chip" href="#/claims/{f.subject_id}">the claim</a>
					{:else if f.subject_kind === 'source' && f.subject_id}
						<button class="chip" onclick={() => openFindingSource(f)}>the source</button>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	{#if app.attention.loaded && total === 0}
		<div class="empty">
			<div class="big">Nothing needs attention</div>
			<p>The source derivations and lint findings are all in order.</p>
		</div>
	{:else if !app.attention.loaded && app.connection !== 'live'}
		<div class="empty">
			<div class="big">Nothing loaded</div>
			<p>Thought Keeper is not reachable, so this is not a statement about the vault.</p>
		</div>
	{/if}
	{#if app.attention.notice}<p class="muted">{app.attention.notice}</p>{/if}
</div>

<style>
	.attention-finding { display: flex; align-items: baseline; gap: 9px; padding: 7px 0; border-bottom: 1px solid var(--border); }
	.attention-msg { flex: 1; min-width: 0; color: var(--text-2); font-size: 13px; }
</style>
