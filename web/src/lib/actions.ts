import { api, ApiError } from './api';
import { goto, parseHash, sourceHash, withParam, withoutParams } from './nav';
import { app, type ConfirmRequest } from './state.svelte';
import type { AtomList, Pipeline, Run, SourceList, WikiPage } from './types';

const msg = (e: unknown): string => (e instanceof ApiError ? e.message : String(e));

let toastTimer: ReturnType<typeof setTimeout> | undefined;
/** toast shows a short, transient message at the bottom of the screen. */
export function toast(text: string): void {
	app.ui.toast = text;
	clearTimeout(toastTimer);
	toastTimer = setTimeout(() => (app.ui.toast = null), 3200);
}

/* ── Load guards ────────────────────────────────────────── */
/* Two requests for the same container can be in flight at once (a burst of events, a user
 * action while an event arrives). Responses do not arrive in the order they were sent, and a
 * slow older one would otherwise overwrite a newer one — the UI then shows what was true
 * before the change, and with no polling it stays that way. Every loader therefore carries a
 * generation and drops its result when a newer request has started. */
const gen = new Map<string, number>();

function begin(key: string): number {
	const token = (gen.get(key) ?? 0) + 1;
	gen.set(key, token);
	return token;
}

function stale(key: string, token: number): boolean {
	return gen.get(key) !== token;
}

/* ── Status ─────────────────────────────────────────────── */
export async function loadPipeline(): Promise<void> {
	try {
		app.pipeline = await api.pipeline();
	} catch {
		/* the status is a convenience: a failed read leaves the last known one */
	}
}

export async function loadStatus(): Promise<void> {
	const token = begin('status');
	try {
		const st = await api.status();
		if (stale('status', token)) return;
		app.status = st;
	} catch {
		if (!stale('status', token)) app.status = null;
	}
}

/* ── The window the UI runs in ──────────────────────────── */

/** loadClaims loads only the newest X claims — the default list is bounded, never the whole vault.
 *  Filtering by a topic is the one thing that changes which window is read. */
export const CLAIM_PAGE_SIZE = 50;

export async function loadClaims(topic = ''): Promise<void> {
	app.claims.topic = topic;
	const token = begin('claims');
	try {
		const res = await api.atoms(topic, CLAIM_PAGE_SIZE);
		if (stale('claims', token)) return;
		app.claims.items = res.atoms ?? [];
		app.claims.total = res.count ?? app.claims.items.length;
		app.claims.loaded = true;
		app.claims.notice = '';
	} catch (e) {
		if (stale('claims', token)) return;
		app.claims.items = [];
		app.claims.total = 0;
		app.claims.notice = 'Could not load claims: ' + msg(e);
	}
}

/** loadClaimTopics loads the topic cloud from the CLAIM catalogue (not from pages). */
export async function loadClaimTopics(): Promise<void> {
	const token = begin('claim-topics');
	try {
		const res = await api.claimTopics();
		if (stale('claim-topics', token)) return;
		const topics = (res.topics ?? []).sort((a, b) => b.count - a.count).slice(0, 24);
		if (JSON.stringify(app.claims.claimTopics) !== JSON.stringify(topics)) app.claims.claimTopics = topics;
	} catch {
		if (!stale('claim-topics', token) && app.claims.claimTopics.length) app.claims.claimTopics = [];
	}
}

/** openClaim loads one claim and its related claims. The cluster peers come first; the shared topic
 *  is the fallback, so a claim that no cluster owns yet still has neighbours. */
export async function openClaim(id: string): Promise<void> {
	app.claims.selected = null;
	app.claims.related = [];
	app.claims.notice = '';
	const token = begin('claim');
	try {
		const [atom, related] = await Promise.all([api.atom(id), api.atomRelated(id).catch(() => ({ atoms: [], count: 0 }))]);
		if (stale('claim', token)) return;
		app.claims.selected = atom;
		app.claims.related = related.atoms ?? [];
	} catch (e) {
		if (stale('claim', token)) return;
		app.claims.notice = 'Could not load claim: ' + msg(e);
	}
}

/** openSource opens a source PAGE. The route owns the tab and the open part/file. */
export function openSource(s: { hash: string; title?: string; kind?: string; ext?: string }): void {
	const { id } = parseHash(location.hash);
	if (id === s.hash) return;
	goto(sourceHash(s.hash));
}

/** loadSource loads the source-page header (title, kind, ext) for the route. The page itself is
 *  rendered by SourceViewer; it is not an overlay. */
export async function loadSource(hash: string): Promise<void> {
	try {
		const s = await api.source(hash);
		if (parseHash(location.hash).id !== hash) return;
		app.ui.viewer = { hash: s.hash, title: s.title || s.hash.slice(0, 12), kind: s.kind, ext: s.ext };
	} catch {
		if (parseHash(location.hash).id === hash) app.ui.viewer = null;
	}
}

export function closeViewer(): void {
	app.ui.viewer = null;
	app.ui.viewerTab = app.ui.viewerPart = app.ui.viewerFile = '';
	goto('#/sources');
}

/** setViewerState changes the source page's tab, open outline part or open file, in the address. */
export function setViewerState(next: { tab?: string; part?: string; file?: string }): void {
	const v = app.ui.viewer;
	if (!v) return;
	if (next.tab !== undefined) {
		app.ui.viewerTab = next.tab;
		app.ui.viewerPart = app.ui.viewerFile = '';
	}
	if (next.part !== undefined) app.ui.viewerPart = next.part;
	if (next.file !== undefined) app.ui.viewerFile = next.file;
	goto(sourceHash(v.hash, { tab: app.ui.viewerTab, part: app.ui.viewerPart, file: app.ui.viewerFile }));
}

/** openSearch and openAdd put the overlays in the address too: a palette or a dialog is a place the
 *  reader was, and back should return to it. */
export function openSearch(): void {
	app.ui.search = true;
	goto(withParam('search', '1'));
}

export function closeSearch(): void {
	app.ui.search = false;
	goto(withoutParams('search'));
}

export function openAdd(): void {
	app.ui.add = true;
	goto(withParam('add', '1'));
}

export function closeAdd(): void {
	app.ui.add = false;
	goto(withoutParams('add'));
}

/** openSourceAt opens a source page AT one of its outline parts — the shape a claim's provenance
 *  carries. The part is part of the address (see nav.sourceHash), so a reload and back/forward return
 *  to the same node. Without a node it is the plain source page. */
export function openSourceAt(hash: string, node = ''): void {
	goto(sourceHash(hash, node ? { tab: 'snapshot', part: node } : {}));
}

/** openSourceByHash opens a source page from its hash alone — the shape a lint finding carries. */
export function openSourceByHash(hash: string): void {
	openSourceAt(hash);
}

/* ── Sources ────────────────────────────────────────────── */
export async function loadSources(): Promise<void> {
	const token = begin('sources');
	try {
		const res = await api.sources();
		if (stale('sources', token)) return;
		app.sources.items = res.sources ?? [];
	} catch {
		if (!stale('sources', token)) app.sources.items = [];
	}
}

export async function addPathOrURL(value: string): Promise<void> {
	const v = value.trim();
	if (!v) return;
	app.sources.notice = v.startsWith('http') ? 'Fetching URL…' : 'Adding…';
	try {
		const src = v.startsWith('http') ? await api.addURL(v) : await api.addPath(v);
		toast(`Added ${src.kind} · ${src.title || src.hash.slice(0, 12)}`);
		app.ui.add = false;
	} catch (e) {
		app.sources.notice = 'Error: ' + msg(e);
		return;
	}
	app.sources.notice = '';
	await Promise.all([loadSources(), loadStatus()]);
}

/** addManyURLs queues a batch of URLs (e.g. a bookmarks list) for background intake. */
export async function addManyURLs(lines: string[]): Promise<void> {
	const urls = lines.map((s) => s.trim()).filter((s) => /^https?:\/\//i.test(s));
	if (urls.length === 0) {
		toast('No URLs found in the input');
		return;
	}
	app.ui.add = false;
	app.sources.notice = '';
	try {
		const res = await api.addURLs(urls);
		toast(`Queued ${res.accepted} URL${res.accepted === 1 ? '' : 's'} — sources appear as they are fetched`);
	} catch (e) {
		toast('URL import error: ' + msg(e));
		return;
	}
	await Promise.all([loadSources(), loadStatus()]);
}

export async function uploadFiles(files: FileList | File[]): Promise<void> {	const list = Array.from(files);
	for (const file of list) {
		app.sources.notice = `Uploading ${file.name}…`;
		try {
			await api.addUpload(file);
		} catch (e) {
			app.sources.notice = '';
			toast(`Upload failed: ${msg(e)}`);
			return;
		}
	}
	app.sources.notice = '';
	toast(`Uploaded ${list.length} file${list.length === 1 ? '' : 's'}`);
	app.ui.add = false;
	await Promise.all([loadSources(), loadStatus()]);
}

export async function refreshURLs(): Promise<void> {
	app.sources.notice = 'Refreshing URLs…';
	try {
		const res = await api.refreshURLs();
		toast(`URLs checked ${res.checked} · changed ${res.changed} · failed ${res.failed}`);
	} catch (e) {
		toast('Error: ' + msg(e));
	}
	app.sources.notice = '';
	await Promise.all([loadSources(), loadStatus()]);
}

/** setRunningStep marks a source as being derived in the step it was asked for, wherever it is
 *  shown: a pressed button must look pressed from the click, not from the first log line. */
function setRunningStep(hash: string, step: string): void {
	const row = app.sources.items.find((x) => x.hash === hash);
	if (row) row.running_step = step;
}

/** askConfirm asks in the app's own dialog — never a native browser alert. */
export function askConfirm(req: ConfirmRequest): void {
	app.ui.confirm = req;
}

export function deleteSource(hash: string): void {
	const s = app.sources.items.find((x) => x.hash === hash);
	const label = s?.title || hash.slice(0, 12);
	askConfirm({
		title: `Delete “${label}”?`,
		message: 'Its claims and their provenance go too. The snapshot moves to the trash.',
		confirmLabel: 'Delete',
		danger: true,
		onConfirm: () => doDeleteSource(hash, label),
	});
}

async function doDeleteSource(hash: string, label: string): Promise<void> {
	try {
		await api.deleteSource(hash);
		toast(`Deleted ${label}`);
	} catch (e) {
		toast('Delete error: ' + msg(e));
		return;
	}
	await Promise.all([loadSources(), loadStatus()]);
}

/** distill continues an unfinished derivation; reset throws it away and starts over. */
export function distill(hash: string, reset = false): void {
	const s = app.sources.items.find((x) => x.hash === hash);
	const label = s?.title || hash.slice(0, 12);

	// A "continue" on a source that has nothing left to continue would silently do nothing
	// (the run ends with 0 atoms and 0 pages). Say so, and offer the full re-derivation.
	if (!reset && s && s.atom_count > 0) {
		const partsLeft = Math.max(0, (s.parts_total ?? 0) - (s.parts_done ?? 0));
		const pending = partsLeft > 0 || (s.atoms_left ?? 0) > 0;
		if (!pending) {
			askConfirm({
				title: `Nothing left to continue for “${label}”.`,
				message:
					'Everything is derived. Derive it again from scratch? The claims are replaced.',
				confirmLabel: 'Derive again',
				danger: true,
				onConfirm: () => doDistill(hash, true),
			});
			return;
		}
	}

	if (reset) {
		const s = app.sources.items.find((x) => x.hash === hash);
		const label = s?.title || hash.slice(0, 12);
		askConfirm({
			title: `Derive “${label}” again from scratch?`,
			message: 'The source\u2019s claims are replaced and derived again.',
			confirmLabel: 'Derive again',
			danger: true,
			onConfirm: () => doDistill(hash, reset),
		});
		return;
	}
	void doDistill(hash, reset);
}

async function doDistill(hash: string, reset: boolean): Promise<void> {
	// The banner names the STEP, not a generic run: the four steps are steered individually, and
	// "distill…" says what the app is doing.
	app.banner = { op: 'distill', status: 'running' };
	// Optimistic: the row shows a running step the moment the button is pressed. Runs are
	// serialized, so waiting for the server would leave the row unchanged for as long as the
	// previous run takes.
	setRunningStep(hash, 'distill');
	let runId = '';
	try {
		runId = (await api.distill(hash, reset)).run_id;
	} catch (e) {
		app.banner = null;
		// A conflict is not an error: the source is already queued or running (that is what
		// auto-continue does). Say that instead of shouting "Distill error".
		if (e instanceof ApiError && e.code === 'conflict') {
			toast('Already running — it is being derived.');
		} else {
			toast('Distill error: ' + msg(e));
		}
		// Server truth: the row keeps showing the spinner while a run is in flight.
		await loadSources();
		return;
	}
	await awaitRun(runId);
	app.banner = null;
	await refreshView();
}

/** capture re-runs step 1 for one source: read it again (extract, render, name). The outline follows,
 *  so the four steps stay in order. */
export async function capture(hash: string): Promise<void> {
	try {
		const res = await api.capture(hash);
		if (res.queued) {
			toast('Capturing — the outline follows');
			setRunningStep(hash, 'capture');
		} else {
			toast(res.detail || 'Nothing to capture');
		}
	} catch (e) {
		toast('Capture error: ' + msg(e));
	}
	await refreshView('sources');
}

/** clusterClaims asks for the cluster step (step 4) by hand: it groups the claims no cluster owns
 *  yet, and waits until no document step is running. */
export async function clusterClaims(): Promise<void> {
	try {
		const res = await api.cluster();
		toast(res.queued ? 'Cluster pass queued' : 'Nothing to cluster — every claim is associated');
	} catch (e) {
		toast('Cluster error: ' + msg(e));
	}
	await Promise.all([loadSources(), loadClaims(app.claims.topic), loadStatus()]);
}

/** reconcile runs the consistency job by hand: check that what the steps claim is on disk, repair
 *  what is not, and ask the cluster step for a pass if there is something for it to do. */
export async function reconcile(): Promise<void> {
	try {
		const res = await api.reconcile();
		const bits = [`checked ${res.checked}`];
		if (res.invalidated?.length) bits.push(`${res.invalidated.length} invalidated`);
		if (res.broken?.length) bits.push(`${res.broken.length} broken`);
		if (res.languages) bits.push(`${res.languages} language(s) detected`);
		toast('Consistency: ' + bits.join(' · '));
	} catch (e) {
		toast('Consistency error: ' + msg(e));
	}
	await Promise.all([loadSources(), loadFindings(), loadStatus()]);
}

/* ── Ask ────────────────────────────────────────────────── */
/** loadAsks refreshes the saved Ask conversations. It is bounded by the API; the view never loads
 *  an unbounded history. */
export async function loadAsks(): Promise<void> {
	const token = begin('asks');
	try {
		const res = await api.asks();
		if (stale('asks', token)) return;
		app.asks.items = res.asks ?? [];
		app.asks.loaded = true;
		app.asks.notice = '';
	} catch (e) {
		if (stale('asks', token)) return;
		app.asks.items = [];
		app.asks.loaded = false;
		app.asks.notice = 'Could not load saved asks: ' + msg(e);
	}
}

/** openAsk loads one saved question and answer. */
export async function openAsk(id: string): Promise<void> {
	app.asks.selectedId = id;
	app.asks.selected = null;
	app.asks.notice = '';
	const token = begin('ask');
	try {
		const ask = await api.ask(id);
		if (stale('ask', token) || app.asks.selectedId !== id) return;
		app.asks.selected = ask;
	} catch (e) {
		if (stale('ask', token) || app.asks.selectedId !== id) return;
		app.asks.notice = 'Could not load the saved ask: ' + msg(e);
	}
}

/** deleteAsk removes one saved ask after the app's own confirmation. */
export function deleteAsk(id: string): void {
	const ask = app.asks.items.find((x) => x.id === id) ?? app.asks.selected;
	askConfirm({
		title: 'Delete this saved ask?',
		message: `“${ask?.question ?? 'This question'}” and its answer are moved to .trash/asks/.`,
		confirmLabel: 'Delete',
		danger: true,
		onConfirm: async () => {
			const wasCurrent = app.asks.currentId === id;
			try {
				await api.deleteAsk(id);
				if (app.asks.selectedId === id) {
					app.asks.selectedId = '';
					app.asks.selected = null;
					goto('#/ask');
				}
				if (wasCurrent) clearAsk();
				await loadAsks();
			} catch (e) {
				toast('Delete ask failed: ' + msg(e));
			}
		},
	});
}

export async function askQuery(question: string): Promise<void> {
	const { id: savedId } = parseHash(location.hash);
	if (savedId) goto('#/ask');
	app.ask.question = question;
	app.ask.answer = '';
	app.ask.notice = 'Thinking…';
	app.ask.busy = true;
	app.asks.selected = null;
	app.asks.selectedId = '';
	app.asks.currentId = '';
	app.banner = { op: 'query', status: 'running' };
	let runId = '';
	try {
		runId = (await api.query(question)).run_id;
		app.ask.runId = runId;
	} catch (e) {
		app.ask.notice = 'Query error: ' + msg(e);
		app.ask.busy = false;
		app.banner = null;
		return;
	}
	// Poll the run itself: the answer appears even if no SSE frame arrives.
	void (async () => {
		await awaitRun(runId, 300_000);
		await finishAsk(runId);
		app.banner = null;
	})();
}

export async function finishAsk(runId: string): Promise<void> {
	// The poller and the SSE run event can both arrive. The first one owns the transition.
	if (!app.ask.busy) return;
	try {
		const run = await api.run(runId);
		// The question lives on the RUN (it is written when the run is created), so an answer that
		// arrives after a reload still knows what was asked.
		if (!app.ask.question && run.question) app.ask.question = run.question;
		if (run.status !== 'succeeded') {
			app.ask.notice = `Query ${run.status}: ${run.error || ''}`;
			app.ask.busy = false;
		} else {
			app.ask.answer = run.answer || '(empty answer)';
			app.ask.notice = `${run.iterations} steps · ${run.total_tokens} tokens`;
			app.asks.currentId = runId;
			app.ask.busy = false;
			await loadAsks();
			// A finished answer is a saved Ask detail, not the history list. Navigate BEFORE selecting
			// anything: selecting first changes the route key inside the old history entry, and the
			// scroll memory would restore that entry's list position onto the new detail page.
			if (app.view === 'ask' && parseHash(location.hash).id !== runId) goto('#/ask/' + runId);
		}
	} catch (e) {
		app.ask.notice = 'Could not read the run: ' + msg(e);
		app.ask.busy = false;
	}
}

/** migrateURLAddresses moves URL sources onto their URL's address.
 *
 *  A URL source used to be addressed by the hash of its FIRST snapshot — a fact about content that is a
 *  moving target, which is what made a refreshed page a second source. The address is the URL now. The
 *  move is a rename: the claims keep their node, their body and their pages, only the address they cite
 *  changes, and nothing is derived again. */
export async function migrateURLAddresses(): Promise<void> {
	try {
		const res = await api.migrateURLAddresses();
		if (res.moved === 0) {
			toast('Every URL source is already at its URL address');
		} else {
			const pending = res.pending?.length ? ` · ${res.pending.length} still pending` : '';
			toast(`Moved ${res.moved} URL source(s) · ${res.atoms_repointed} claim(s) re-pointed${pending}`);
		}
		await resyncAll();
	} catch (e) {
		toast('Moving URL addresses failed: ' + msg(e));
	}
}

/** fixURLs repairs sources whose index entry lost the fact that they are URL snapshots. */
export async function fixURLs(): Promise<void> {
	try {
		const res = await api.fixURLs();
		toast(
			res.fixed > 0
				? `Fixed ${res.fixed} of ${res.checked} checked source(s) — they are URLs again`
				: `Nothing to fix: ${res.checked} Markdown source(s) checked, none declares a URL`,
		);
		await Promise.all([loadSources(), loadStatus()]);
	} catch (e) {
		toast('Fix URLs failed: ' + msg(e));
	}
}

/** loadAtomLayout reads what a vault still holds in the OLD claim layout (branch folders): a folder
 *  whose name is a legacy topic, holding claims named after a slug instead of the claim's ID. */
export async function loadAtomLayout(): Promise<void> {
	try {
		app.vault.atomLayout = await api.atomLayout();
	} catch {
		app.vault.atomLayout = null;
	}
}

/** loadVocabulary reads the health of the name vocabularies: how many names, how much of the knowledge
 *  the head carries, what is mechanically wrong, and what a curation has not seen yet. */
export async function loadVocabulary(): Promise<void> {
	try {
		const res = await api.vocabulary();
		app.vault.vocabulary = res.namespaces ?? [];
	} catch {
		app.vault.vocabulary = null;
	}
}

/** curateVocabulary asks the model what one name vocabulary should become and applies the answer.
 *
 *  The vocabulary is the agent's — nothing about it is manual — so this is one action, not a preview and
 *  an apply: the report beside the button IS the preview (the collisions, the near-duplicates, the long
 *  tail). What the run does is a RENAME: a claim keeps its text and its node, a page keeps its prose and
 *  its claims, and only the label changes (the inline #tag included). */
/** rebranchClaims re-derives the BRANCH label of every claim from the claim itself. A branch is the
 *  subject of ONE claim; when a curation collapsed the branch vocabulary into umbrellas, the labels lost
 *  the level they exist for. This is the repair: no claim is rewritten, no page touched — the model
 *  reads the claims and names each one's subject. Bounded per run; the rest follows on the next press. */
export async function rebranchClaims(): Promise<void> {
	try {
		await api.vocabularyRebranch();
		toast('Re-labelling the claims — the topics are re-derived from the claim texts');
	} catch (e) {
		toast('Re-topic failed: ' + msg(e));
	}
}

export async function curateVocabulary(): Promise<void> {
	try {
		await api.vocabularyCurate();
		toast('Curating the claim-topic vocabulary…');
	} catch (e) {
		toast('Curation failed: ' + msg(e));
	}
}

/** loadAtomDuplicates reads what exists more than once: the same claim written several times. */
export async function loadAtomDuplicates(): Promise<void> {
	try {
		app.vault.atomDuplicates = await api.atomDuplicates();
	} catch {
		app.vault.atomDuplicates = null;
	}
}

/** collapseAtoms keeps one copy of every claim that exists more than once and moves the rest to the
 *  vault's .trash/atoms/. The copies are indistinguishable — same statement, same provenance, same
 *  body — so nothing is lost by keeping one. A copy a page carries is the one that stays. */
export async function collapseAtoms(): Promise<void> {
	try {
		const res = await api.collapseAtoms();
		if (res.collapsed === 0) {
			toast('Nothing to collapse: every claim exists once');
		} else {
			toast(`Kept one copy of ${res.groups} claim(s), moved ${res.collapsed} to the trash`);
		}
		await loadAtomDuplicates();
	} catch (e) {
		toast('Collapse failed: ' + msg(e));
	}
}

/** reorganizeAtoms moves those claims to `atoms/<ab>/<cd>/<id>.md` — the way a source is addressed by
 *  its hash — and removes the folders that empty out. A button, not a start-up habit: the vault is the
 *  user's, and moving their files is something they ask for. Idempotent. */
export async function reorganizeAtoms(): Promise<void> {
	try {
		const res = await api.reorganizeAtoms();
		if (res.moved === 0) {
			toast('Nothing to move: no claim folder left in the old layout');
		} else {
			const idx = res.indexes_removed ? ` · ${res.indexes_removed} branch index(es) removed` : '';
			const kept = res.kept?.length ? ` · ${res.kept.length} folder(s) kept (unreadable files)` : '';
			toast(`Moved ${res.moved} claim(s) out of ${res.folders_removed} folder(s)${idx}${kept}`);
		}
		await loadAtomLayout();
	} catch (e) {
		toast('Reorganize failed: ' + msg(e));
	}
}

/** clearAsk empties the Ask view: the question, the answer and the run behind them. Nothing in the
 *  vault is touched — saved Ask records stay in asks/, this only clears the current view. */
export function clearAsk(): void {
	app.ask.runId = null;
	app.ask.question = '';
	app.ask.answer = '';
	app.ask.notice = '';
	app.asks.currentId = '';
}

/* ── Attention (the actionable state of the vault) ──────── */
/** loadAttention gathers what needs a human decision: failed source steps and lint findings. It is a
 *  lens over existing endpoints, not a second health model. */
export async function loadAttention(): Promise<void> {
	const token = begin('attention');
	try {
		await Promise.all([loadSources(), loadFindings()]);
		if (stale('attention', token)) return;
		app.attention.loaded = true;
		app.attention.notice = '';
	} catch (e) {
		if (stale('attention', token)) return;
		app.attention.loaded = false;
		app.attention.notice = 'Could not load attention: ' + msg(e);
	}
}

/* ── Wiki ───────────────────────────────────────────────── */
export async function loadWiki(): Promise<void> {
	const token = begin('wiki');
	try {
		const res = await api.wiki();
		if (stale('wiki', token)) return;
		app.wiki.pages = res.pages ?? [];
		app.wiki.tags = res.tags ?? [];
		app.wiki.loaded = true;
		app.wiki.notice = '';
	} catch (e) {
		if (stale('wiki', token)) return;
		app.wiki.notice = 'Could not load the wiki: ' + msg(e);
	}
}

export async function openWiki(id: string): Promise<void> {
	const token = begin('wiki-page');
	app.wiki.selectedId = id;
	app.wiki.newPage = false;
	if (app.wiki.selected?.id !== id) app.wiki.selected = null;
	try {
		const page = await api.wikiPage(id);
		if (stale('wiki-page', token)) return;
		app.wiki.selected = page;
		app.wiki.notice = '';
	} catch (e) {
		if (stale('wiki-page', token)) return;
		app.wiki.selected = null;
		app.wiki.notice = 'Could not load this wiki page: ' + msg(e);
	}
}

/** saveWikiPage writes the page and then opens what the server stored. The page's source is
 *  reprocessed by the pipeline; the response is the page, not the run. */
export async function saveWikiPage(
	id: string,
	inPage: { title: string; tags: string[]; body: string },
): Promise<WikiPage | null> {
	try {
		const page = await api.saveWiki(id, inPage);
		await loadWiki();
		app.wiki.selected = page;
		app.wiki.selectedId = page.id;
		app.wiki.newPage = false;
		app.wiki.notice = '';
		toast('Wiki page saved — its source is being reprocessed');
		goto('#/wiki/' + page.id);
		return page;
	} catch (e) {
		app.wiki.notice = 'Could not save the wiki page: ' + msg(e);
		return null;
	}
}

export function deleteWikiPage(id: string): void {
	askConfirm({
		title: 'Delete this wiki page?',
		message: 'The page and its derived source/claims are removed. The source bytes go to the trash; the page file is deleted.',
		confirmLabel: 'Delete',
		danger: true,
		onConfirm: async () => {
			try {
				await api.deleteWiki(id);
				app.wiki.selected = null;
				app.wiki.selectedId = '';
				await loadWiki();
				toast('Wiki page deleted');
				goto('#/wiki');
			} catch (e) {
				app.wiki.notice = 'Could not delete the wiki page: ' + msg(e);
			}
		},
	});
}

/* ── Status / lint ──────────────────────────────────────── */
export async function loadFindings(): Promise<void> {
	const token = begin('findings');
	try {
		const res = await api.findings();
		if (stale('findings', token)) return;
		const next = res.findings ?? [];
		if (JSON.stringify(app.lint.findings) !== JSON.stringify(next)) app.lint.findings = next;
	} catch {
		if (!stale('findings', token)) app.lint.findings = [];
	}
}

export async function runLint(): Promise<void> {
	app.lint.notice = 'Running…';
	app.banner = { op: 'lint', status: 'running' };
	let runId = '';
	try {
		runId = (await api.lint()).run_id;
	} catch (e) {
		app.lint.notice = '';
		app.banner = null;
		toast('Lint error: ' + msg(e));
		return;
	}
	const run = await awaitRun(runId);
	await loadFindings();
	app.banner = null;
	app.lint.notice =
		run?.status === 'succeeded'
			? run.answer || `${app.lint.findings.length} findings`
			: `lint ${run?.status ?? 'failed'}: ${run?.error ?? ''}`;
}

/** cleanup repairs the association layer: prunes dead cluster members and resolves claim links
 *  (no agent). */
export async function cleanup(): Promise<void> {
	app.lint.notice = 'Cleaning up…';
	try {
		const res = await api.cleanup();
		toast(`Cleanup: ${res.clusters_pruned} cluster(s) pruned, ${res.clusters_removed} removed, ${res.links_repaired} link(s) repaired · ${res.findings} finding(s)`);
	} catch (e) {
		toast('Cleanup error: ' + msg(e));
		app.lint.notice = '';
		return;
	}
	app.lint.notice = '';
	await Promise.all([loadFindings(), loadSources(), loadStatus()]);
}

/** resetSource throws a source's derivation away — tree, claims, pages — and starts over.
 *  The source itself (bytes, id, metadata) stays; everything derived is built again. */
export function resetSource(hash: string): void {
	const s = app.sources.items.find((x) => x.hash === hash);
	const label = s?.title || hash.slice(0, 12);
	askConfirm({
		title: `Reset “${label}”?`,
		message:
			'The outline and the claims of this source are thrown away and built again from the stored file. The source itself stays; the next cluster pass regroups what remains.',
		confirmLabel: 'Reset',
		danger: true,
		onConfirm: () => doResetSource(hash),
	});
}

async function doResetSource(hash: string): Promise<void> {
	app.banner = { op: 'reset', status: 'running' };
	setRunningStep(hash, 'capture');
	try {
		const res = await api.resetSource(hash);
		toast(`Reset: ${res.nodes} node(s), ${res.atoms} claim(s) gone`);
	} catch (e) {
		setRunningStep(hash, '');
		app.banner = null;
		toast('Reset error: ' + msg(e));
		await loadSources();
		return;
	}
	// The pipeline restarts by itself: capture (if the source needs it), outline, then distill.
	await refreshView();
}

/** rebuildTree rebuilds the tree of one source (per file, on demand). */
export async function rebuildTree(hash: string): Promise<void> {
	app.banner = { op: 'outline', status: 'running' };
	try {
		const res = await api.rebuildTree(hash);
		toast(`Outline rebuilt: ${res.nodes} node(s), ${res.pages || 0} page(s)${res.ocr ? ' · OCR' : ''}`);
	} catch (e) {
		app.banner = null;
		toast('Rebuild outline error: ' + msg(e));
		return;
	}
	app.banner = null;
	await refreshView();
}

/** rebuildTrees rebuilds every source's tree (the repair pass). */
export async function rebuildTrees(): Promise<void> {
	app.lint.notice = 'Rebuilding outlines…';
	try {
		const res = await api.rebuildTrees();
		toast(`Outlines rebuilt: ${res.built}/${res.sources}${res.failed ? ` · ${res.failed} failed` : ''}`);
	} catch (e) {
		app.lint.notice = '';
		toast('Rebuild outlines error: ' + msg(e));
		return;
	}
	app.lint.notice = '';
	await Promise.all([loadSources(), loadStatus()]);
}

/** reindex rebuilds the index from the vault: sources, claims and clusters, and it prunes
 *  orphaned rows. The vault is the truth, so this is always safe to run. */
export async function reindex(): Promise<void> {
	app.lint.notice = 'Reindexing…';
	try {
		const res = await api.reindex();
		toast(
			`Reindex: ${res.atoms} claim(s), ${res.clusters} cluster(s)` +
				(res.skipped ? `, ${res.skipped} skipped` : '') +
				(res.orphans ? `, ${res.orphans} orphan row(s) removed` : '') +
				(res.atoms_orphaned ? `, ${res.atoms_orphaned} claim(s) trashed — every source they cited is gone` : '') +
				(res.provenance_pruned ? `, ${res.provenance_pruned} claim(s) lost a dead source` : '') +
				(res.clusters_removed ? `, ${res.clusters_removed} empty cluster(s) removed` : '')
		);
	} catch (e) {
		toast('Reindex error: ' + msg(e));
		app.lint.notice = '';
		return;
	}
	app.lint.notice = '';
	await Promise.all([loadFindings(), loadSources(), loadStatus()]);
}

/* ── Runs ───────────────────────────────────────────────── */
/** awaitRun polls a run until it stops running. UI-initiated actions use this so
 * their status is deterministic and does not depend on an SSE frame arriving. */
async function awaitRun(runId: string, timeoutMs = 180_000): Promise<Run | null> {
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		let run: Run;
		try {
			run = await api.run(runId);
		} catch {
			return null;
		}
		if (run.status !== 'running') return run;
		if (Date.now() > deadline) return run;
		await new Promise((r) => setTimeout(r, 700));
	}
}


/* ── Search (quick find) ────────────────────────────────── */
let searchTimer: ReturnType<typeof setTimeout> | undefined;
export function scheduleSearch(q: string): void {
	app.search.q = q;
	clearTimeout(searchTimer);
	const term = q.trim();
	if (term.length < 2) {
		app.search.hits = [];
		app.search.active = 0;
		return;
	}
	searchTimer = setTimeout(() => void runSearch(term), 160);
}

async function runSearch(term: string): Promise<void> {
	app.search.busy = true;
	try {
		const res = await api.search(term, 'atoms');
		// A newer keystroke owns the palette now; this answer is for a query nobody asks anymore.
		if (app.search.q.trim() !== term) return;
		app.search.hits = res.hits ?? [];
		app.search.active = 0;
	} catch {
		if (app.search.q.trim() === term) app.search.hits = [];
	}
	app.search.busy = false;
}

/** finishLint loads the run's result into the lint notice and the findings list. */
async function finishLint(runId: string): Promise<void> {
	try {
		const run = await api.run(runId);
		await loadFindings();
		app.lint.notice =
			run.status === 'succeeded'
				? run.answer || `${app.lint.findings.length} findings`
				: `lint ${run.status}: ${run.error || ''}`;
	} catch {
		/* ignore */
	}
}

/* ── SSE-driven refresh ─────────────────────────────────── */
/* Events arrive in bursts (a run emits hundreds of log lines), so they are NOT handled one
 * by one as they arrive: they are collected, folded into the store, and then each container
 * that actually changed is reloaded exactly once. Handling frames concurrently is how a stale
 * response wins the race — and with no polling that would be permanent. */
const inbox: Array<{ type: string; data: Record<string, unknown> }> = [];
let draining = false;

/** enqueueEvent queues one SSE frame for the next batch. */
export function enqueueEvent(type: string, data: Record<string, unknown>): void {
	inbox.push({ type, data });
	if (!draining) void drainEvents();
}

async function drainEvents(): Promise<void> {
	draining = true;
	try {
		while (inbox.length > 0) {
			const batch = inbox.splice(0, inbox.length);
			let all = false;
			const scopes = new Set<RefreshScope>();
			for (const frame of batch) {
				let scope: RefreshScope | null = null;
				try {
					scope = await absorbEvent(frame.type, frame.data);
				} catch {
					// One malformed frame must not stop the ones behind it.
				}
				// 'all' covers every container; a narrower scope next to it adds nothing.
				if (scope === 'all') {
					all = true;
					scopes.clear();
				} else if (scope && !all) {
					scopes.add(scope);
				}
			}
			if (all) await refreshView('all');
			else for (const scope of scopes) await refreshView(scope);
		}
	} finally {
		draining = false;
	}
}

/** absorbEvent folds one frame into the store and reports the container it invalidated.
 *  It never fetches — the batch decides what to reload. */
async function absorbEvent(type: string, data: Record<string, unknown>): Promise<RefreshScope | null> {
	if (type === 'run') {
		// The chip names the STEP, not the class: the four steps are steered individually, so
		// "distill…" / "cluster…" is what the app is actually doing.
		app.banner = { op: String(data.step || data.op || ''), status: String(data.status ?? '') };
		if (data.status === 'running') {
			// The run announced itself; the row already shows it (the app publishes a source
			// event when the run is queued). No full reload per frame.
			return null;
		}
		app.banner = null;
		if (data.status === 'failed') {
			// A dead run must not just vanish: say which one failed and why.
			const why = String(data.error ?? '').split('\n')[0];
			toast(`${String(data.op ?? 'run')} failed${why ? `: ${why}` : ' — see the source log'}`);
		}
		// A finished run hands the row back to the derived counters.
		for (const hash of Object.keys(app.sources.status)) {
			const row = app.sources.items.find((x) => x.hash === hash);
			if (!row || !row.running_step) delete app.sources.status[hash];
		}
		const runID = String(data.run_id ?? '');
		if (String(data.op) === 'lint') {
			await finishLint(runID);
			return null; // finishLint already loaded the findings
		}
		if (app.ask.runId && runID === app.ask.runId) {
			await finishAsk(runID);
			return null;
		}
		// A MAINTENANCE run (a name curation, a re-sort) has no step light to watch: its report is
		// the only thing that says what it did, and it has to be re-read. Without this the Status page
		// kept showing the vocabulary the run had just replaced until the reader reloaded the page.
		const op = String(data.op ?? '');
		if (op === 'vocabulary') return 'vocabulary';
		return 'all';
	}
	if (type === 'findings') {
		const count = Number(data.count ?? NaN);
		if (!Number.isNaN(count)) app.lint.notice = `${count} findings`;
		return 'findings';
	}
	if (type === 'progress') {
		// Only the counts. Each event carries whichever numbers its step knows; they are merged
		// per source, so the row and the detail view always show the latest known state.
		const hash = String(data.hash ?? '');
		const fields = (data.fields ?? {}) as Record<string, unknown>;
		if (!hash) return null;
		const counts: Record<string, unknown> = { ...(app.sources.status[hash] ?? {}) };
		for (const [key, value] of Object.entries(fields)) {
			if (typeof value === 'number') counts[key] = value;
		}
		// The phase and what it is doing: the row shows the numbers, the detail view shows both.
		if (typeof data.phase === 'string') counts.phase = data.phase;
		if (typeof data.msg === 'string') counts.msg = data.msg;
		app.sources.status[hash] = counts;
		return null;
	}
	if (type === 'log') {
		// Live tail: the running document's log line arrives as it is written, so the overlay
		// shows what the run is doing instead of waiting for it to finish.
		const hash = String(data.hash ?? '');
		const line = typeof data.line === 'string' ? data.line : '';
		if (line && app.ui.sourceLog && app.ui.sourceLog.hash === hash) {
			const text = (app.ui.sourceLog.text + line).split('\n').slice(-600).join('\n');
			app.ui.sourceLog = { hash, text };
		}
		return null;
	}
	if (type === 'pipeline') {
		// The event CARRIES the status, so no fetch is needed: the bar is always one frame behind
		// at most, and a burst of pipeline events costs nothing.
		const p = data.pipeline as Pipeline | undefined;
		if (p) app.pipeline = p;
		return null;
	}
	if (type === 'run') {
		// A maintenance run (a curation, a re-sort) has no step light to watch: its outcome is the
		// only thing that says whether it worked, and a button that says nothing at all is a button
		// nobody can trust. (This block used to sit below the live `run` handler and was dead code —
		// the `if` above it always returned first.)
		const op = String(data.op ?? '');
		const status = String(data.status ?? '');
		if (op === 'vocabulary' && (status === 'succeeded' || status === 'failed')) {
			const err = String(data.error ?? '');
			toast(status === 'failed' ? `${op} failed: ${err || 'see the log'}` : `${op} done`);
		}
		return null;
	}
	if (type === 'source' || type === 'urls') return 'sources';
	if (type === 'wiki') return 'wiki';
	if (type === 'clusters') return 'all';
	if (type === 'vocabulary') {
		// The curation publishes this on EVERY phase, not only at the end: the numbers beside the
		// button are live while the run works through the catalog, and a run that dies halfway still
		// shows what it merged.
		return 'vocabulary';
	}
	return null;
}

/** refreshView reloads the current view's data (events carry no payloads). */
/** Scope of a refresh: only the containers an event actually changed. */
export type RefreshScope = 'all' | 'sources' | 'claims' | 'findings' | 'vocabulary' | 'wiki';

/** refreshView reloads what an event changed — never more. A full reload on every frame is
 *  what makes a list jump and an open page flicker while a long run is going. */
export async function refreshView(scope: RefreshScope = 'all'): Promise<void> {
	// An open source page re-reads its claims: a run finishing while the page
	// is open must show up there, not only in the list behind it.
	app.ui.sourceVersion++;
	if (scope === 'all' || scope === 'sources' || scope === 'claims') {
		await Promise.all([loadStatus(), loadPipeline()]);
	}
	switch (scope) {
		case 'sources':
			await loadSources();
			return;
		case 'findings':
			await loadFindings();
			return;
		case 'claims':
			await loadClaims(app.claims.topic);
			await loadClaimTopics();
			if (app.claims.selected) await openClaim(app.claims.selected.id);
			return;
		case 'vocabulary':
			// A curation changed names: the report beside the button must not keep showing the
			// vocabulary it just replaced.
			await loadVocabulary();
			return
		case 'wiki':
			await loadWiki();
			if (app.wiki.selectedId) await openWiki(app.wiki.selectedId);
			return;
		case 'all':
			switch (app.view) {
				case 'claims':
					await loadClaims(app.claims.topic);
					await loadClaimTopics();
					break;
				case 'ask':
					await loadAsks();
					break;
				case 'attention':
					await loadAttention();
					break;
				case 'sources':
				case 'bookmarks':
					await loadSources();
					break;
				case 'status':
					await loadFindings();
					break
				case 'wiki':
					await loadWiki();
					if (app.wiki.selectedId) await openWiki(app.wiki.selectedId);
					break;
			}
	}
}

/** resyncAll is the catch-up after a dropped event stream: one full reload. */
export async function resyncAll(): Promise<void> {
	app.ui.sourceVersion++;
	await loadStatus();
	await Promise.all([loadSources(), loadFindings(), loadWiki()]);
	if (app.view === 'claims') {
		await loadClaims(app.claims.topic);
		await loadClaimTopics();
	}
	if (app.view === 'ask') await loadAsks();
	if (app.view === 'attention') await loadAttention();
	if (app.view === 'wiki') await loadWiki();
}
