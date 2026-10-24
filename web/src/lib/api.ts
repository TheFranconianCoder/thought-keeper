import type {
	Atom,
	AtomDuplicates,
	AtomLayout,
	AtomList,
	TopicUseList,
	DeleteResult,
	FindingList,
	CaptureResult,
	ClusterResult,
	DistillResponse,
	ReconcileResult,
	VocabularyHealth,
	Pipeline,
	RefreshResult,
	Run,
	SearchResult,
	Source,
	SourceList,
	WikiList,
	WikiPage,
	SourceNode,
	Status,
	AskList,
	SavedAsk,
} from './types';

const API = '/api/v1';

/** ApiError carries the coded error the server sends (see payloads.ErrorBody). */
export class ApiError extends Error {
	constructor(
		public code: string,
		message: string,
	) {
		super(message);
		this.name = 'ApiError';
	}
}

/** reqText fetches a plain-text endpoint (the per-source log). */
async function reqText(path: string): Promise<string> {
	const resp = await fetch(API + path);
	if (!resp.ok) throw new ApiError('internal', resp.statusText);
	return await resp.text();
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
	const resp = await fetch(API + path, init);
	const body = await resp.json().catch(() => null);
	if (!resp.ok) {
		const err = body?.error as { code?: string; message?: string } | undefined;
		throw new ApiError(err?.code ?? 'internal', err?.message ?? resp.statusText);
	}
	return body as T;
}

const json = (method: string, value?: unknown): RequestInit => ({
	method,
	headers: { 'Content-Type': 'application/json' },
	body: value === undefined ? undefined : JSON.stringify(value),
});

export const api = {
	status: () => req<Status>('/status'),
	// The global pipeline status: documents (capture/outline/distill) or the cluster step. The four
	// steps are strictly separated, so this is one answer, not a list.
	pipeline: () => req<Pipeline>('/pipeline'),

	sources: (kind = '') => req<SourceList>('/sources' + (kind ? `?kind=${encodeURIComponent(kind)}` : '')),
	source: (hash: string) => req<Source>(`/sources/${hash}`),
	sourceTree: (hash: string) => req<{ hash: string; nodes: SourceNode[] }>(`/sources/${hash}/tree`),
	/** sourceNode is ONE part of the outline, with its text: the tree is the table of contents, and
	 *  this is how a reader walks it — the same way the agent does. */
	sourceNode: (hash: string, nodeID: string) =>
		req<SourceNode>(`/sources/${hash}/nodes/${encodeURIComponent(nodeID)}`),
	sourceAtoms: (hash: string) => req<AtomList>(`/sources/${hash}/atoms`),
	/** fixURLs repairs sources whose index entry lost the fact that they are URL snapshots: the
	 *  snapshot's own frontmatter names the address. */
	fixURLs: () => req<{ checked: number; fixed: number }>('/fix-urls', { method: 'POST' }),
	/** vocabulary reads the health of the claim-topic vocabulary. */
	vocabulary: () => req<{ namespaces: VocabularyHealth[] }>('/vocabulary'),
	/** vocabularyCurate is the whole curation: baseline, then the claim topics. */
	vocabularyRebranch: () =>
		req<{ run_id: string; status: string }>('/vocabulary/retopic', { method: 'POST' }),
	vocabularyCurate: () =>
		req<{ run_id: string; status: string }>('/vocabulary/curate', { method: 'POST' }),
	/** urlAddresses previews the URL address migration: what is still on the old address. */
	urlAddresses: () => req<{ checked: number; count: number; pending?: string[] }>('/urls/addresses'),
	/** migrateURLAddresses moves URL sources onto their URL's address (a rename, not a re-derivation). */
	migrateURLAddresses: () =>
		req<{ checked: number; moved: number; atoms_repointed: number; skipped: number; pending?: string[] }>(
			'/urls/migrate',
			{ method: 'POST' },
		),
	deleteSource: (hash: string) => req<DeleteResult>(`/sources/${hash}`, { method: 'DELETE' }),

	addPath: (path: string) => req<Source>('/add', json('POST', { path })),
	addURL: (url: string) => req<Source>('/add', json('POST', { url })),
	addURLs: (urls: string[]) => req<{ accepted: number }>('/add-urls', json('POST', { urls })),
	addUpload: (file: File) => {
		const fd = new FormData();
		fd.append('file', file);
		return req<Source>('/add', { method: 'POST', body: fd });
	},
	refreshURLs: () => req<RefreshResult>('/refresh-urls', { method: 'POST' }),

	// The DOCUMENT run of one source: outline if needed → distill. The cluster step has its own
	// endpoint — it runs over the whole vault, never per source.
	capture: (hash: string) => req<CaptureResult>(`/sources/${hash}/capture`, { method: 'POST' }),
	// The consistency job by hand: the same pass the periodic worker runs.
	reconcile: () => req<ReconcileResult>('/reconcile', { method: 'POST' }),
	// The cluster step (step 4) by hand.
	cluster: () => req<ClusterResult>('/cluster', { method: 'POST' }),
	distill: (hash: string, reset = false) =>
		req<DistillResponse>(`/sources/${hash}/distill`, json('POST', { reset })),

	/** wiki lists the user-authored pages and their tag cloud. */
	wiki: () => req<WikiList>('/wiki'),
	wikiPage: (id: string) => req<WikiPage>(`/wiki/${id}`),
	/** saveWiki creates (POST) or updates (PUT) one page. A save also reprocesses its source. */
	saveWiki: (id: string, page: { title: string; tags: string[]; body: string }) =>
		req<WikiPage>(id ? `/wiki/${id}` : '/wiki', json(id ? 'PUT' : 'POST', page)),
	deleteWiki: (id: string) => req<{ id: string }>(`/wiki/${id}`, { method: 'DELETE' }),

	claimTopics: () => req<TopicUseList>('/topics/claims'),
	/** atoms lists claims (newest first), optionally for one topic. The list is deliberately bounded. */
	atoms: (topic = '', limit = 50) =>
		req<AtomList>(`/atoms?${topic ? `topic=${encodeURIComponent(topic)}&` : ''}limit=${limit}`),
	atomRelated: (id: string, limit = 20) => req<AtomList>(`/atoms/${id}/related?limit=${limit}`),
	/** asks lists the auto-saved Ask conversations, newest first. */
	asks: () => req<AskList>('/asks'),
	ask: (id: string) => req<SavedAsk>(`/asks/${id}`),
	deleteAsk: (id: string) => req<{ id: string }>(`/asks/${id}`, { method: 'DELETE' }),
	/** atom reads ONE claim: its provenance (the source nodes it came from) and its associations.
	 *  A lint finding points at a claim, and this is how the reader looks at it. */
	atom: (id: string) => req<Atom>(`/atoms/${id}`),
	/** atomLayout is the claim LAYOUT: the branch folders a vault written before the ID layout still
	 *  holds (GET), and the move to the ID layout (POST). */
	atomLayout: () => req<AtomLayout>('/atoms/layout'),
	reorganizeAtoms: () => req<AtomLayout>('/atoms/reorganize', { method: 'POST' }),
	/** atomDuplicates is what exists more than once (GET) and the collapse that keeps one copy (POST). */
	atomDuplicates: () => req<AtomDuplicates>('/atoms/duplicates'),
	collapseAtoms: () => req<AtomDuplicates>('/atoms/collapse', { method: 'POST' }),
	search: (q: string, layer = 'atoms', limit = 20) => req<SearchResult>(`/search?q=${encodeURIComponent(q)}&layer=${layer}&limit=${limit}`),

	/** rawURL is the inline bytes endpoint for a source snapshot. */
	rawURL: (hash: string) => `${API}/sources/${hash}/raw`,
	/** faviconURL is the inline icon endpoint for a source. */
	faviconURL: (hash: string) => `${API}/sources/${hash}/favicon`,
	/** rawText fetches a text-like file of a source for inline display. `which` selects the readable
	 *  rendering ("render") instead of the object — the text step 1 extracted, which is what a PDF
	 *  source shows while the PDF itself stays one click away — or one of the derived files the Files
	 *  view lists (".meta", ".outline", ".log"). */
	rawText: async (hash: string, which?: string): Promise<string> => {
		const resp = await fetch(API + `/sources/${hash}/raw` + (which ? `?which=${which}` : ''));
		if (!resp.ok) throw new ApiError('internal', resp.statusText);
		return resp.text();
	},

	query: (question: string) => req<DistillResponse>('/query', json('POST', { question })),
	run: (id: string) => req<Run>(`/runs/${id}`),

	lint: () => req<DistillResponse>('/lint', { method: 'POST' }),
	findings: (kind = '') => req<FindingList>('/lint/findings' + (kind ? `?kind=${kind}` : '')),
	cleanup: () =>
		req<{
			clusters_pruned: number;
			clusters_removed: number;
			links_repaired: number;
			findings: number;
		}>('/cleanup', { method: 'POST' }),
	sourceLog: (hash: string) => reqText(`/sources/${hash}/log`),
	resetSource: (hash: string) =>
		req<{ hash: string; atoms: number; pages: number; nodes: number; bookmark?: boolean }>(
			`/sources/${hash}/reset`,
			{ method: 'POST' }
		),
	rebuildTree: (hash: string) =>
		req<{ hash: string; nodes: number; pages: number; title: string; ocr: boolean }>(
			`/sources/${hash}/rebuild-tree`,
			{ method: 'POST' }
		),
	reindex: () =>
		req<{
			scanned: number;
			indexed: number;
			added: number;
			removed: number;
			atoms: number;
			clusters: number;
			clusters_removed: number;
			skipped: number;
			orphans: number;
			atoms_orphaned?: number;
			provenance_pruned?: number;
		}>('/reindex', { method: 'POST' }),
	rebuildTrees: () =>
		req<{ sources: number; built: number; failed: number }>('/rebuild-trees', { method: 'POST' }),
};
