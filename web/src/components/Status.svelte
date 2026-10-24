<script lang="ts">
	import { app } from '../lib/state.svelte';
	import {
		cleanup,
		clusterClaims,
		loadFindings,
		rebuildTrees,
		reconcile,
		reindex,
		collapseAtoms,
		fixURLs,
		refreshURLs,
		migrateURLAddresses,
		loadAtomDuplicates,
		loadAtomLayout,
		loadVocabulary,
		curateVocabulary,
		rebranchClaims,
		openSourceByHash,
		reorganizeAtoms,
		runLint,
	} from '../lib/actions';
	import { api } from '../lib/api';
	import Icon from './Icon.svelte';
	import type { Atom, Finding } from '../lib/types';
	import { onMount } from 'svelte';

	// The claim a finding is about, once the reader asked for it: a lint finding must point at its
	// origin, and for a claim that origin is the claim itself — shown HERE, because an atom is not a
	// place in the app (it is what a page and a source are made of).
	let atom = $state<Atom | null>(null);
	let atomError = $state('');

	const actionableFindings = $derived(app.lint.findings.filter((f) => f.severity !== 'info'));
	const infoFindings = $derived(app.lint.findings.filter((f) => f.severity === 'info'));

	/** The work in flight, as the app reports it: one row per source it holds, one for the cluster step,
	 *  one per ask and lint. The counts elsewhere say how busy the app is; this says what it is busy
	 *  WITH — and what a run REPORTS while it works (its phase, its parts) comes from the same live
	 *  progress the Sources view reads, joined by the source's hash. */
	const jobs = $derived(app.pipeline?.jobs ?? []);

	/** How many QUEUED rows the Job Queue shows before it folds the rest into a count. A batch of a
	 *  hundred links makes a hundred queued rows; on their own they buried the one row that was
	 *  working, and a busy app looked like a stuck one. Running and waiting always show. */
	const JOB_QUEUE_SHOWN = 8;
	const jobRows = $derived.by(() => {
		const head = jobs.filter((j) => j.state === 'running' || j.state === 'waiting');
		const queued = jobs.filter((j) => j.state === 'queued' || j.state === 'pending');
		const tail = jobs.filter(
			(j) =>
				j.state !== 'running' &&
				j.state !== 'waiting' &&
				j.state !== 'queued' &&
				j.state !== 'pending',
		);
		return { rows: [...head, ...queued.slice(0, JOB_QUEUE_SHOWN), ...tail], hidden: Math.max(0, queued.length - JOB_QUEUE_SHOWN) };
	});

	/** liveOf is what a source's run is doing right now: the phase it published, with its counts. */
	function liveOf(hash: string | undefined): string {
		if (!hash) return '';
		const p = app.sources.status[hash] ?? {};
		const phase = typeof p.phase === 'string' ? p.phase : '';
		const msg = typeof p.msg === 'string' ? p.msg : '';
		const done = typeof p.parts_done === 'number' ? p.parts_done : null;
		const total = typeof p.parts_total === 'number' ? p.parts_total : null;
		const parts = done !== null && total ? ` · ${done}/${total} parts` : '';
		if (phase === 'queued') return msg || 'waiting';
		if (msg) return `${msg}${parts}`;
		if (phase) return `${phase}${parts}`;
		return parts ? parts.replace(' · ', '') : '';
	}

	// The elapsed time has to move on its own. `Date.now()` is not a reactive dependency, so a label
	// computed from it sat at "7s" until something else happened to re-render the row — the clock is
	// what makes it a clock. One tick a second, and only while a run is claimed: an idle Status page
	// does no work at all.
	let clock = $state(Date.now());
	$effect(() => {
		if (!jobs.some((j) => j.since)) return;
		const t = setInterval(() => (clock = Date.now()), 1000);
		return () => clearInterval(t);
	});

	/** sinceOf turns the claim time into "for 2m 10s" — a run claimed far too long is the one in
	 *  trouble, and a timestamp alone makes the reader do the subtraction. `now` is passed in so the
	 *  label is a function of the ticking clock and not of an invisible `Date.now()`. */
	function sinceOf(iso: string | undefined, now: number): string {
		if (!iso) return '';
		const started = Date.parse(iso);
		if (Number.isNaN(started)) return '';
		const secs = Math.max(0, Math.round((now - started) / 1000));
		if (secs < 60) return `${secs}s`;
		const mins = Math.floor(secs / 60);
		return `${mins}m ${secs % 60}s`;
	}

	/** stepOf names the step the way the rest of the UI does, and what it runs over. */
	function stepOf(step: string): string {
		switch (step) {
			case 'capture': return 'capture';
			case 'outline': return 'outline';
			case 'distill': return 'distill';
			case 'cluster': return 'cluster claims';
			case 'query': return 'ask';
			case 'lint': return 'lint';
			// The maintenance passes: they are not steps of the pipeline, but they are work the app is
			// doing right now, and the Running list is where a reader looks for that.
			case 'vocabulary': return 'curate names';
			case 'reindex': return 'reindex';
			case 'reconcile': return 'reconcile';
			case 'cleanup': return 'cleanup';
			case 'rebuild-trees': return 'rebuild outlines';
			case 'refresh-urls': return 'refresh URLs';
			default: return step;
		}
	}

	async function showAtom(id: string): Promise<void> {
		if (atom?.id === id) {
			atom = null;
			return;
		}
		atomError = '';
		try {
			atom = await api.atom(id);
		} catch {
			atomError = 'this claim is not in the index any more';
		}
	}

	onMount(() => {
		if (app.lint.findings.length === 0) void loadFindings();
		void loadAtomLayout();
		void loadAtomDuplicates();
		void loadVocabulary();
	});

	/** busy reports whether the job a button starts is already in flight (queued, waiting or running).
	 *  Pressing it again would queue a second pass over the same vault — the button goes inactive
	 *  instead, and says why. */
	const busy = (job: string): boolean =>
		(app.pipeline?.jobs ?? []).some(
			(j) => j.step === job && (j.state === 'running' || j.state === 'queued' || j.state === 'waiting'),
		);

	const caps = $derived.by(() => {
		const s = app.status;
		if (!s) return [] as { label: string; ok: boolean; why?: string }[];
		return [
			{ label: 'pdf', ok: s.tools.pdf },
			{ label: 'ocr', ok: s.tools.ocr },
			{ label: 'agent', ok: s.agent.configured },
			{ label: 'vault', ok: s.vault.writable },
			{ label: 'search', ok: s.index.search === 'fts5' },
			{
				// The drop zone still works when the kernel refuses a watch — it is polled instead. Saying
				// "off" there was the read that made "how do I add a file?" unanswerable.
				label: 'inbox',
				ok: s.app.inbox?.watched ?? true,
				why: s.app.inbox?.error
					? `polled instead of watched — ${s.app.inbox.error}`
					: undefined,
			},
		];
	});

	/** The maintenance actions, grouped by what they belong to: the cluster step, the vault's
	 *  consistency, and the health checks. Each one says what it does. */
	const clusterActions = [
		{
			label: 'Cluster claims',
			job: 'cluster',
			hint: 'Step 4 by hand: group the claims no cluster owns yet (it waits until no document step runs).',
			run: () => clusterClaims(),
		},
	];
	const vaultActions = [
		{
			label: 'Check consistency',
			job: 'reconcile',
			hint: 'Walk the vault and verify that what the step records claim is on disk; a missing outline invalidates its derivation and re-queues the source.',
			run: () => reconcile(),
		},
		{
			label: 'Reindex',
			job: 'reindex',
			hint: 'Rebuild the index from the vault: reads every source, claim and cluster record, drops rows whose file is gone, and prunes orphaned rows.',
			run: () => reindex(),
		},
		{
			label: 'Fix URLs',
			job: 'fix-urls',
			hint: 'Repair sources that lost the fact that they are URL snapshots: their frontmatter still names the address they came from.',
			run: () => fixURLs(),
		},
		{
			label: 'Refresh URL sources',
			job: 'refresh-urls',
			hint: 'Re-fetch every known URL once. A changed page is corrected through its existing source; an unchanged page only updates its status and last-check time.',
			run: () => refreshURLs(),
		},
		{
			label: 'Rebuild outlines',
			job: 'rebuild-trees',
			hint: 'Rebuild every source’s outline (step 2) from its capture — no extraction, no model.',
			run: () => rebuildTrees(),
		},
		{
			label: 'Collapse duplicate claims',
			job: 'collapse-atoms',
			hint: 'Keep one copy of every claim that exists more than once — same statement, same provenance, same body — and move the rest to .trash/atoms/. A copy a cluster owns is the one that stays; two claims that only look alike (different sources) both stay.',
			run: () => collapseAtoms(),
		},
		{
			label: 'Move URL sources to their URL address',
			job: 'migrate-urls',
			hint: 'A URL source is addressed by its URL now, not by the hash of its first snapshot — the snapshot is what a refresh replaces, the URL is what stays. This moves every URL source still on the old address: it is a rename, the claims keep their node and their pages, only the address they cite changes, and nothing is derived again.',
			run: () => migrateURLAddresses(),
		},
		{
			label: 'Reorganize claims',
			job: 'reorganize-atoms',
			hint: 'Move claims out of the legacy topic folders to atoms/<ab>/<cd>/<id>.md — addressed by their ID, the way a source is addressed by its hash — and remove the folders that empty out. The generated legacy indexes go with them (they catalog a folder, and there are none), and a stale atoms/index.md is deleted: the claim catalog is the index, not a file. Claims stay exactly what they are; only the file names change.',
			run: () => reorganizeAtoms(),
		},
	];
</script>

{#snippet findingRow(f: Finding)}
	<div class="finding">
		<span class="sev">{f.severity}</span>
		<div class="fbody">
			<div class="fmsg">
				<span>{f.kind}: {f.message}</span>
				<span class="forigin">
					{#if f.subject_kind === 'source' && f.subject_id}
						<button class="src-link" onclick={() => openSourceByHash(f.subject_id!)}>
							<Icon name="db" size={12} /> the source
						</button>
					{:else if f.subject_kind === 'atom' && f.subject_id}
						<button class="src-link" onclick={() => showAtom(f.subject_id!)}>
							<Icon name="distill" size={12} />
							{atom && atom.id === f.subject_id ? 'hide the claim' : 'the claim'}
						</button>
					{/if}
				</span>
			</div>
			{#if atom && atom.id === f.subject_id}
				<div class="fatom">
					<div class="claim">{atom.claim}</div>
					<div class="fmeta">
						<span class="chip">{atom.topic}</span>
						<span class="chip">{atom.type}</span>
						{#if atom.status !== 'active'}<span class="chip chip-dirty">{atom.status}</span>{/if}
					</div>
					{#if atom.sources?.length}
						<div class="fmeta">
							<span class="muted">from</span>
							{#each atom.sources as s (s.hash + s.node)}
								<button class="src-link" onclick={() => openSourceByHash(s.hash)}>
									<Icon name="db" size={12} /> {s.hash.slice(0, 8)}{#if s.node}<span class="muted">·{s.node}</span>{/if}{#if s.ocr}<span class="muted">·ocr</span>{/if}
								</button>
							{/each}
						</div>
					{:else}
						<div class="fmeta muted">This claim cites no source — it cannot be attributed.</div>
					{/if}
					<div class="fmeta muted">
						{atom.cluster_count ? `In ${atom.cluster_count} cluster${atom.cluster_count === 1 ? '' : 's'} — open the claim to see its peers.` : 'Not in a cluster yet.'}
					</div>
				</div>
			{/if}
			{#if atomError && f.subject_kind === 'atom'}
				<span class="muted" style="font-size:12px">{atomError}</span>
			{/if}
		</div>
	</div>
{/snippet}

<div class="status">
	<div class="page-head"><h1>Status</h1></div>

	{#if app.status}
		{@const s = app.status}
		<div class="stat-grid">
			<div class="stat"><div class="num">{s.index.sources}</div><div class="lbl">sources</div></div>
			<div class="stat"><div class="num">{s.index.atoms}</div><div class="lbl">claims</div></div>
			<div class="stat"><div class="num">{s.index.clusters}</div><div class="lbl">clusters</div></div>
			<div class="stat"><div class="num">{s.index.unclustered}</div><div class="lbl">unclustered</div></div>
			<div class="stat"><div class="num">{s.index.runs}</div><div class="lbl">runs</div></div>
		</div>

		<div style="display:flex;gap:8px;margin-bottom:20px">
			{#each caps as c (c.label)}
				<span class={c.ok ? 'chip chip-accent' : 'chip'} title={c.why ?? ''}
					>{c.label} {c.ok ? 'ok' : 'polling'}</span
				>
			{/each}
		</div>

		<div class="block-title">Job Queue</div>
		{#if jobs.length === 0}
			<p class="muted">Nothing is running — no document step, no cluster pass, no ask.</p>
		{:else}
			<div class="jobs">
				{#each jobRows.rows as j (j.step + j.state + (j.target ?? ''))}
					<div class="job">
						<span class="jstep">{stepOf(j.step)}</span>
						<span class="jstate {j.state}" title={j.why || ''}>
							{j.state === 'pending' ? 'owed' : j.state}
						</span>
						{#if j.target}
							<!-- A document step names its source, and one click opens it: the row answers
							     "which source is being processed?" and then shows it. -->
							<button class="src-link" onclick={() => openSourceByHash(j.target!)}>
								<Icon name="db" size={12} /> {j.title || j.target.slice(0, 12)}
							</button>
						{:else}
							<span class="muted">'over the whole vault'</span>
						{/if}
						{#if j.detail}<span class="jlive">{j.detail}</span>{/if}
						{#if j.why && !liveOf(j.target)}<span class="jlive">{j.why}</span>{/if}
						{#if liveOf(j.target)}<span class="jlive">{liveOf(j.target)}</span>{/if}
						<span class="spacer"></span>
						{#if j.since}<span class="muted" title={j.since}>{sinceOf(j.since, clock)}</span>{/if}
					</div>
				{/each}
				{#if jobRows.hidden > 0}
					<div class="job">
						<span class="muted">{jobRows.hidden} more queued — they run as the workers reach them</span>
					</div>
				{/if}
			</div>
		{/if}

		<div class="block-title">Clusters</div>
		<div class="actions">
			{#each clusterActions as a (a.label)}
				<div class="action">
					<button
						class="btn"
						disabled={busy(a.job)}
						title={busy(a.job) ? `${a.label} is already running` : a.hint}
						onclick={a.run}>{a.label}</button
					>
					<span class="muted">{a.hint}</span>
				</div>
			{/each}
		</div>

		<div class="block-title">
			Vocabulary
			<span class="spacer"></span>
			<button
				class="btn btn-accent"
				disabled={busy('vocabulary')}
				title={busy('vocabulary') ? 'The curation is already running' : 'Curate the claim-topic vocabulary: adopt the baseline, then ask the model what the CLAIM TOPICS should become. A rename only; nothing is derived again.'}
				onclick={() => curateVocabulary()}
			>
				<Icon name="spark" /> Curate
			</button>
			<button
				class="btn"
				disabled={busy('vocabulary')}
				title={busy('vocabulary')
					? 'A vocabulary run is already running'
					 : 'Re-derive the TOPIC of every claim from the claim itself. A topic is the flat subject of one claim; the repair changes only that label. No claim text is rewritten. Bounded per run; the rest follows on the next press.'}
				onclick={() => rebranchClaims()}
			>
				<Icon name="distill" /> Re-topic claims
			</button>
		</div>
		<!-- The names the agent invented at write time: they grow with every run and drift apart
		     (caching, cache-invalidation, Caching Strategies). These are the numbers that say WHERE to
		     look — never what to merge: a name used once can be the sharpest name there is. -->
		<p class="muted" style="font-size:12.5px;margin:0 0 10px">
			<b>Curate</b> is one unit: it adopts the baseline (the names that exist — the vault is the
			truth about that), asks the model what the <b>claim topics</b> should become, and applies that
			as a <b>rename</b>. A claim keeps its text, its node and its associations; only the label
			changes. Topics are FLAT: a topic is the subject of one claim, never a folder, and there is
			no domain level.
		</p>
		{#if app.vault.vocabulary && app.vault.vocabulary.length}
			<div class="vocab">
				{#each app.vault.vocabulary as v (v.namespace)}
					<div class="vocab-row">
						<span class="vocab-ns">{v.namespace}</span>
						<span class="vocab-nums">
							{v.names} names · {v.uses} uses · {v.singletons} once
							({Math.round(v.singleton_share * 100)}%) · top {Math.round(v.coverage * 100)}%
							{#if v.canonical || v.absorbed}· {v.canonical} canonical, {v.absorbed} absorbed{/if}
							{#if v.new?.length}· <b>{v.new.length} new</b>{/if}
						</span>
						{#if v.collisions?.length}
							<span class="vocab-find" title="two spellings of one name — always a bug">
								{v.collision_count ?? v.collisions.length} collision(s)
							</span>
						{/if}
						{#if v.near_duplicates?.length}
							<span class="vocab-find" title="names that share their words — a question worth asking">
								{v.near_duplicate_count ?? v.near_duplicates.length} family/families of shared words
							</span>
						{/if}
						{#if v.broad_tokens}
							<span class="vocab-find" title="learned from this vault: ignored by near-duplicate detection">
								{v.broad_tokens} broad token(s)
							</span>
						{/if}
						{#if v.oversized_families}
							<span class="vocab-find" title="too broad to merge: counted, never suggested">
								{v.oversized_families} broad families ignored
							</span>
						{/if}
					</div>
				{/each}
			</div>
		{:else}
			<p class="muted" style="font-size:12.5px">No vocabulary report yet.</p>
		{/if}

		<div class="block-title">Vault</div>
		{#if app.vault.atomDuplicates && app.vault.atomDuplicates.groups > 0}
			<!-- Claims that exist more than once: a write that could not be recorded left its file behind.
			     Named with a count and one example, so the number can be recognised before it is acted on. -->
			<p class="muted">
				{app.vault.atomDuplicates.files} claim file(s) in {app.vault.atomDuplicates.groups} group(s)
				exist more than once{#if app.vault.atomDuplicates.example} — e.g.
					“{app.vault.atomDuplicates.example}”{/if}: the same statement, the same provenance, the
				same body. The copies are indistinguishable; collapsing keeps one of them.
			</p>
		{/if}
		{#if app.vault.atomLayout && (app.vault.atomLayout.folders?.length ?? 0) > 0}
			<!-- The old claim layout, named: a folder whose name is a legacy topic, holding claims named after
			     a slug. The app writes `atoms/<ab>/<cd>/<id>.md` now; the button moves what is left. -->
			<p class="muted">
				{app.vault.atomLayout.claims} claim(s) are still in the old layout
				({app.vault.atomLayout.folders?.length} legacy topic folder(s)
				({app.vault.atomLayout.folders?.slice(0, 6).join(', ')}{#if (app.vault.atomLayout.folders?.length ?? 0) > 6}, …{/if})
				, or a claim in the atoms root) — the layout the app wrote before claims were addressed
				by their ID{#if app.vault.atomLayout.indexes}, with
					{app.vault.atomLayout.indexes} generated index file(s) that go with them{/if}.
			</p>
		{/if}
		<div class="actions">
			{#each vaultActions as a (a.label)}
				<div class="action">
					<button
						class="btn"
						disabled={busy(a.job)}
						title={busy(a.job) ? `${a.label} is already running` : a.hint}
						onclick={a.run}>{a.label}</button
					>
					<span class="muted">{a.hint}</span>
				</div>
			{/each}
		</div>
	{/if}

	<div class="block-title">
		Knowledge health
		<span class="spacer"></span>
		<button
			class="btn btn-accent"
			disabled={busy('lint')}
			title={busy('lint') ? 'A lint run is already running' : 'Run the programmatic health checks over the claims and the vocabulary.'}
			onclick={() => runLint()}
		>
			<Icon name="search" /> Run lint
		</button>
		<button
			class="btn"
			disabled={busy('cleanup')}
			title={busy('cleanup') ? 'Cleanup is already running' : 'Prune dead cluster members and repair claim links without the agent.'}
			onclick={() => cleanup()}
		>
			<Icon name="spark" /> Cleanup
		</button>
	</div>
	<p class="muted">{app.lint.notice}</p>
	{#if app.lint.findings.length === 0}
		<p class="muted">No findings.</p>
	{:else}
		{#each actionableFindings as f (f.id)}
			{@render findingRow(f)}
		{/each}
		{#if infoFindings.length}
			<details class="info-findings">
				<summary>{infoFindings.length} informational finding{infoFindings.length === 1 ? '' : 's'}</summary>
				{#each infoFindings as f (f.id)}
					{@render findingRow(f)}
				{/each}
			</details>
		{/if}
	{/if}

</div>
