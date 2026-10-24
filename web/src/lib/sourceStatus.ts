import type { Pipeline, Source, StepStatus } from './types';
import { app } from './state.svelte';

/** What a source is doing right now, and how far it has got.
 *
 *  The list row and the detail overlay must say the same thing about the same source — they used to
 *  compute it separately, and the overlay said less. One source of truth for both. */

export interface SourceCounts {
	/** Work units (one text part each) that are done and how many there are. */
	partsDone: number;
	partsTotal: number;
	/** Claims that exist for the source, and claims no cluster owns yet. */
	claims: number;
	open: number;
	/** The step the run is in right now, when one is running (from the live progress events). */
	phase: string;
	msg: string;
	/** How many runs are ahead of this one, when it is queued. */
	queuedAhead: number;
}

/** countsOf merges the derived numbers from the API with the live ones from the run.
 *
 *  While a run is in flight the live values win: `source_atoms` is the whole source's claim count
 *  (the part's own count would jump around), `parts_*` are the finished work units, `atoms_left`
 *  is the association backlog. Without this the view showed whatever the API said when it was opened. */
export function countsOf(s: Source): SourceCounts {
	const live = app.sources.status[s.hash] ?? {};
	const num = (key: string, fallback: number): number =>
		typeof live[key] === 'number' ? live[key] : fallback;
	const baseClaims = s.atom_count ?? 0;
	const claims = num('source_atoms', baseClaims);

	// `atoms_left` is the COMPILE backlog. During a document run the progress events carry
	// `source_atoms` (the claims written so far) but not a matching `atoms_left`; the fallback was
	// the last API snapshot. Mixing a live claim count with that stale backlog made the claims bar
	// jump to 100% as soon as the first claim was written, then snap back at the end of the run.
	// Claims added since the snapshot are necessarily un-cited, so they add to the backlog too.
	let open = num('atoms_left', s.atoms_left ?? 0);
	if (claims > baseClaims) open = Math.max(open, (s.atoms_left ?? 0) + (claims - baseClaims));
	open = Math.min(Math.max(0, open), claims);

	return {
		partsTotal: num('parts_total', s.parts_total ?? 0),
		partsDone: num('parts_done', s.parts_done ?? 0),
		claims,
		open,
		phase: typeof live.phase === 'string' ? live.phase : '',
		msg: typeof live.msg === 'string' ? live.msg : '',
		queuedAhead: num('queued_ahead', 0),
	};
}

/** isLive reports whether the numbers above come from a run that is writing them right now. */
export function isLive(s: Source): boolean {
	return app.sources.status[s.hash] !== undefined;
}

/** isWorking reports whether a DOCUMENT step is running for this source right now. The detail view
 *  gets its meta from the API (a snapshot), so it asks the list row too — a run that started while
 *  the overlay was open must not look idle there. */
export function isWorking(s: Source): boolean {
	if (s.running_step) return true;
	const row = app.sources.items.find((x) => x.hash === s.hash);
	return Boolean(row?.running_step);
}

/** runningStep names the document step running for this source right now ("" when none is). */
export function runningStep(s: Source): string {
	const row = app.sources.items.find((x) => x.hash === s.hash);
	return s.running_step || row?.running_step || '';
}

/** statusLabel names the state of a source; the counts follow it.
 *
 *  "complete" is a claim about the DERIVATION, not about one claim existing: a run that derived 3 of
 *  25 parts is not complete, it is partial — and a source that says "complete" while its own detail
 *  view lists open parts is a source nobody can trust. The label is derived like everything else
 *  here, from numbers the payload already carries (parts done/total, claims without a page). */
export function statusLabel(s: Source): string {
	if (s.bookmark) return 'bookmark';
	const step = runningStep(s);
	if (step) {
		// A run that waits in a queue is not executing: saying "distilling…" for it is what made a
		// wait look like a hang while the source's log stayed empty.
		const c = countsOf(s);
		if (c.phase === 'queued') {
			const ahead = c.queuedAhead;
			return ahead > 0 ? `queued · ${ahead} ahead` : 'queued';
		}
		return `${step}…`;
	}
	if (s.atom_count === 0) {
		// Provenance rows without their atoms: the index is damaged. Say so instead of showing a
		// number the detail view cannot show.
		if (s.provenance_rows > 0) return `${s.provenance_rows} orphaned row(s) · Reindex`;
		// A LINK a wiki page declared: the row exists before the page does. Nothing has been fetched
		// while it has no object, and a failed fetch says so with the reason — that is the whole point
		// of keeping the row.
		const capture = s.steps?.capture;
		if (capture?.state === 'failed') {
			const why = (capture.error || capture.detail || '').split('\n')[0].trim();
			const short = why.length > 70 ? `${why.slice(0, 70)}…` : why;
			return short ? `link failed · ${short}` : 'link failed';
		}
		if (capture?.state === 'pending' && !s.path) return 'fetching link…';
		// The STEP RECORD decides, not the tree: a source whose distillation is through and which
		// produced no claim is not "pending distill" — it is finished. Its parts are link lists or
		// document metadata (a link collection says nothing itself, it points at what does), so all
		// four lights are green and there is simply nothing to associate.
		const distill = s.steps?.distill;
		if (distill?.state === 'ok') return 'complete · no claims';
		if (s.tree_state === 'built') return 'pending distill';
		if (s.tree_state === 'failed') return 'outline failed';
		return s.tree_state;
	}
	// Claims exist — but is the derivation finished? A part that is still open (or an incomplete one)
	// and a claim no page cites yet are both "there is work left", and neither is stored: every run
	// re-derives them.
	const c = countsOf(s);
	const partsOpen = c.partsTotal > 0 && c.partsDone < c.partsTotal;
	if (partsOpen || c.open > 0) {
		const bits: string[] = [];
		if (partsOpen) bits.push(`${c.partsDone}/${c.partsTotal} parts`);
		if (c.open > 0) bits.push(`${c.open} open`);
		return `partial · ${bits.join(' · ')}`;
	}
	return `complete · ${s.atom_count} claims`;
}

/** The four pipeline steps, in order: capture → outline → distill → cluster. The first three are
 *  DOCUMENT steps — they run for one source — and cluster is the association step, which runs over
 *  everything and only while no document step runs. */
export const PIPELINE_STEPS = ['capture', 'outline', 'distill', 'cluster'] as const;
export type PipelineStep = (typeof PIPELINE_STEPS)[number];

/** stepLabels names the steps in the UI's words; the letter in the light is the marker. */
export const stepLabels: Record<PipelineStep, string> = {
	capture: 'capture — read the source into a local snapshot',
	outline: 'outline — build the document’s structure',
	distill: 'distill — one claim per atom',
	cluster: 'cluster — group the claims into titleless associations (over all sources)',
};

/** stepsOf returns the four lights of a source, always all four. A step whose state is not in the
 *  payload is pending: the pipeline never hides a step. */
export function stepsOf(s: Source): Record<PipelineStep, StepStatus> {
	const out = {} as Record<PipelineStep, StepStatus>;
	for (const step of PIPELINE_STEPS) {
		out[step] = s.steps?.[step] ?? { state: 'pending' };
	}
	return out;
}

/** stepTitle is the tooltip of one light: what the step is, what it did, and why it failed. */
export function stepTitle(step: PipelineStep, st: StepStatus): string {
	const bits = [stepLabels[step]];
	if (st.detail) bits.push(st.detail);
	if (st.error) bits.push(`failed: ${st.error}`);
	if (st.state === 'pending') bits.push('not done yet');
	if (st.state === 'skipped') bits.push('does not apply');
	if (st.state === 'failed') bits.push('this blocks the next step');
	if (st.state === 'running') bits.push('running now');
	return bits.join(' · ');
}

/** pipelineLabel names what the app is doing RIGHT NOW, globally: the two working states are the
 *  document steps and the cluster step, and they never overlap. */
export function pipelineLabel(p: Pipeline | null): string {
	if (!p) return '';
	const counts = (m: Record<string, number>, ...steps: PipelineStep[]): number =>
		steps.reduce((n, step) => n + (m[step] ?? 0), 0);
	const run = p.running ?? {};
	const queued = p.queued ?? {};
	if ((run.cluster ?? 0) > 0) {
		const n = run.cluster ?? 0;
		return `clustering · ${n} pass${n === 1 ? '' : 'es'}`;
	}
	const docs = counts(run, 'capture', 'outline', 'distill');
	if (docs > 0) {
		const which = PIPELINE_STEPS.filter((s) => s !== 'cluster' && (run[s] ?? 0) > 0);
		return `${which.join(' + ')} · ${docs} source${docs === 1 ? '' : 's'}`;
	}
	const clusterWaiting = queued.cluster ?? 0;
	if (clusterWaiting > 0) return 'cluster pass waiting for the documents';
	const docQueued = counts(queued, 'capture', 'outline', 'distill');
	if (docQueued > 0) return `${docQueued} queued`;
	return '';
}

/** countLabel is the count line: parts, claims and what is still open. */
export function countLabel(s: Source, pages = 0): string {
	const c = countsOf(s);
	const bits: string[] = [];
	if (c.partsTotal > 0) bits.push(`${c.partsDone}/${c.partsTotal} parts`);
	else if (c.partsDone > 0) bits.push(`${c.partsDone} parts`);
	if (c.claims > 0) bits.push(`${c.claims} claims`);
	if (c.open > 0) bits.push(`${c.open} open`);
	return bits.join(' · ');
}

/** totalsLabel is the count line under the lights: how many PARTS and how many CLAIMS a source has.
 *
 *  Totals only — how far along they are is what the two bars above say — and it is ALWAYS shown, so a
 *  finished source still says what it is made of. */
export function totalsLabel(s: Source): string {
	const c = countsOf(s);
	const parts = `${c.partsTotal} ${c.partsTotal === 1 ? 'part' : 'parts'}`;
	const claims = `${c.claims} ${c.claims === 1 ? 'claim' : 'claims'}`;
	return `${parts} · ${claims}`;
}

/** SourceProgress is how far a source's derivation has come: the two bars and their totals. */
export interface SourceProgress {
	/** Percent of the parts that have been distilled (0..100). */
	parts: number;
	/** Percent of the claims a cluster owns (0..100). */
	claims: number;
	partsDone: number;
	partsTotal: number;
	claimsDone: number;
	claimsTotal: number;
}

/** progressOf returns the two progress bars of a source, or null when there is nothing left to show.
 *
 *  The two bars are the two PHASES of the derivation: the PARTS bar is how much of the document has
 *  been distilled, the CLAIMS bar is how much of what was distilled has reached a cluster. They are
 *  always shown together — one bar alone could not say which dimension it is — and only a source that
 *  is FINISHED shows none: "100% and 100%" is exactly "nothing left to derive".
 *
 *  A dimension with nothing to do counts as done: a claim-free document has no claims to associate, and
 *  a source without an outline has no parts yet. Without that rule a finished source would carry a
 *  permanent empty bar. */
export function progressOf(s: Source): SourceProgress | null {
	if (s.bookmark) return null;
	const c = countsOf(s);
	const partsComplete = c.partsTotal === 0 || c.partsDone >= c.partsTotal;
	const partsPct = c.partsTotal > 0 ? c.partsDone / c.partsTotal : 1;
	const claimsPct = c.claims > 0 ? (c.claims - c.open) / c.claims : partsComplete ? 1 : 0;
	if (partsPct >= 1 && claimsPct >= 1) return null;
	return {
		parts: Math.round(partsPct * 100),
		claims: Math.round(claimsPct * 100),
		partsDone: c.partsDone,
		partsTotal: c.partsTotal,
		claimsDone: c.claims - c.open,
		claimsTotal: c.claims,
	};
}
