import { json, request, requestText } from './client';
import type {
  AskList,
  Atom,
  AtomDuplicates,
  AtomLayout,
  AtomList,
  AtomRelated,
  CaptureResult,
  ClaimCode,
  CleanupResult,
  ClusterResult,
  Command,
  CommandContext,
  CommandList,
  DeleteResult,
  DistillResponse,
  FindingList,
  Interest,
  InterestList,
  InterestMatch,
  InterestPreview,
  InterestRequest,
  Pipeline,
  RebuildTreeResult,
  ReconcileResult,
  RefetchResult,
  RefreshResult,
  ReindexResult,
  Run,
  SavedAsk,
  SearchResult,
  Source,
  SourceFacets,
  SourceList,
  SourceListFilter,
  SourceNode,
  SourceReset,
  SourceTree,
  Status,
  TopicUseList,
  VocabularyHealth,
  WikiList,
  WikiPage,
} from './types';

const enc = encodeURIComponent;

const withQuery = (path: string, params: Record<string, string | number | undefined>): string => {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') query.set(key, String(value));
  }
  const q = query.toString();
  return q ? `${path}?${q}` : path;
};

export const api = {
  status: () => request<Status>('/status'),
  pipeline: () => request<Pipeline>('/pipeline'),

  // ── Sources ────────────────────────────────────────────────────────────
  sources: (filter: SourceListFilter = {}, limit = 200, offset = 0, signal?: AbortSignal) =>
    request<SourceList>(
      withQuery('/sources', {
        kind: filter.kind,
        q: filter.query,
        tag: filter.tag,
        command: filter.command,
        attached: filter.attachedOnly ? 1 : undefined,
        bookmarks: filter.bookmarksOnly ? 1 : undefined,
        failed: filter.failedOnly ? 1 : undefined,
        favorite: filter.favoriteOnly ? 1 : undefined,
        readlater: filter.readLaterOnly ? 1 : undefined,
        interest: filter.interest,
        limit,
        offset,
      }),
      { signal },
    ),
  sourceFacets: (signal?: AbortSignal) => request<SourceFacets>('/sources/facets', { signal }),
  source: (hash: string, signal?: AbortSignal) => request<Source>(`/sources/${enc(hash)}`, { signal }),
  sourceTree: (hash: string, signal?: AbortSignal) =>
    request<SourceTree>(`/sources/${enc(hash)}/tree`, { signal }),
  sourceNode: (hash: string, nodeID: string, signal?: AbortSignal) =>
    request<SourceNode>(`/sources/${enc(hash)}/nodes/${enc(nodeID)}`, { signal }),
  sourceAtoms: (hash: string, signal?: AbortSignal) =>
    request<AtomList>(`/sources/${enc(hash)}/atoms`, { signal }),
  sourceChildren: (hash: string, signal?: AbortSignal) =>
    request<SourceList>(`/sources/${enc(hash)}/children`, { signal }),
  sourceSummary: (hash: string, signal?: AbortSignal) =>
    requestText(`/sources/${enc(hash)}/summary`, { signal }),
  generateSourceSummary: (hash: string) =>
    request<DistillResponse>(`/sources/${enc(hash)}/summary`, { method: 'POST' }),
  sourceLog: (hash: string, signal?: AbortSignal) =>
    requestText(`/sources/${enc(hash)}/log`, { signal }),
  rawText: (hash: string, which?: string, signal?: AbortSignal) =>
    requestText(withQuery(`/sources/${enc(hash)}/raw`, { which }), { signal }),
  rawURL: (hash: string) => `/api/v1/sources/${enc(hash)}/raw`,
  faviconURL: (hash: string) => `/api/v1/sources/${enc(hash)}/favicon`,

  addPath: (path: string) => request<Source>('/add', json('POST', { path })),
  addURL: (url: string, bookmark = false) =>
    request<Source>('/add', json('POST', { url, bookmark })),
  addURLs: (urls: string[]) => request<{ accepted: number }>('/add-urls', json('POST', { urls })),
  addUpload: (file: File) => {
    const body = new FormData();
    body.append('file', file);
    return request<Source>('/add', { method: 'POST', body });
  },
  // attachSnapshot gives a URL source the bytes the app could not fetch: a page the human saved from
  // their browser because the site asked for a login or a subscription.
  attachSnapshot: (hash: string, file: File) => {
    const body = new FormData();
    body.append('file', file);
    return request<Source>(`/sources/${enc(hash)}/attach`, { method: 'POST', body });
  },
  refreshURLs: () => request<RefreshResult>('/refresh-urls', { method: 'POST' }),
  refreshSource: (hash: string) =>
    request<RefreshResult>(`/sources/${enc(hash)}/refresh`, { method: 'POST' }),
  // refetchSource tries the URL again for a snapshot the human attached by hand; a wall keeps it.
  refetchSource: (hash: string) =>
    request<RefetchResult>(`/sources/${enc(hash)}/refetch`, { method: 'POST' }),
  fixURLs: () => request<{ checked: number; fixed: number }>('/fix-urls', { method: 'POST' }),
  urlAddresses: () => request<{ checked: number; count: number; pending?: string[] }>('/urls/addresses'),
  migrateURLAddresses: () =>
    request<{ checked: number; moved: number; atoms_repointed: number; skipped: number; pending?: string[] }>(
      '/urls/migrate',
      { method: 'POST' },
    ),

  capture: (hash: string) => request<CaptureResult>(`/sources/${enc(hash)}/capture`, { method: 'POST' }),
  distill: (hash: string, reset = false) =>
    request<DistillResponse>(`/sources/${enc(hash)}/distill`, json('POST', { reset })),
  resetSource: (hash: string) =>
    request<SourceReset>(`/sources/${enc(hash)}/reset`, { method: 'POST' }),
  rebuildTree: (hash: string) =>
    request<RebuildTreeResult>(`/sources/${enc(hash)}/rebuild-tree`, { method: 'POST' }),
  deleteSource: (hash: string) =>
    request<DeleteResult>(`/sources/${enc(hash)}`, { method: 'DELETE' }),
  setSourceTags: (hash: string, tags: string[]) =>
    request<Source>(`/sources/${enc(hash)}/tags`, json('PUT', { tags })),
  // The reader's own marks: the star and the read-later queue. A DELETE ends the intent (the
  // source itself is untouched).
  setSourceFavorite: (hash: string, on: boolean) =>
    request<Source>(`/sources/${enc(hash)}/favorite`, { method: on ? 'PUT' : 'DELETE' }),
  setSourceReadLater: (hash: string, queued: boolean) =>
    request<Source>(`/sources/${enc(hash)}/read-later`, { method: queued ? 'PUT' : 'DELETE' }),
  // Which of the reader's rules this source fits, with the evidence (computed on demand).
  sourceInterests: (hash: string) =>
    request<{ interests: InterestMatch[] }>(`/sources/${enc(hash)}/interests`),

  // ── Interests ──────────────────────────────────────────────────────────
  interests: () => request<InterestList>('/interests'),
  saveInterest: (slug: string, body: InterestRequest) =>
    request<Interest>(slug ? `/interests/${enc(slug)}` : '/interests', json(slug ? 'PUT' : 'POST', body)),
  deleteInterest: (slug: string) =>
    request<{ slug: string }>(`/interests/${enc(slug)}`, { method: 'DELETE' }),
  previewInterest: (body: InterestRequest) =>
    request<InterestPreview>('/interests/preview', json('POST', body)),
  setInterestSource: (slug: string, hash: string, accepted: boolean) =>
    request<{ slug: string; hash: string; accepted: boolean }>(
      `/interests/${enc(slug)}/sources/${enc(hash)}`,
      json('PUT', { accepted }),
    ),

  // ── Wiki ───────────────────────────────────────────────────────────────
  wiki: () => request<WikiList>('/wiki'),
  wikiPage: (id: string) => request<WikiPage>(`/wiki/${enc(id)}`),
  saveWiki: (id: string, page: { title: string; tags: string[]; body: string }) =>
    request<WikiPage>(id ? `/wiki/${enc(id)}` : '/wiki', json(id ? 'PUT' : 'POST', page)),
  deleteWiki: (id: string) => request<{ id: string }>(`/wiki/${enc(id)}`, { method: 'DELETE' }),
  setWikiFavorite: (id: string, on: boolean) =>
    request<WikiPage>(`/wiki/${enc(id)}/favorite`, { method: on ? 'PUT' : 'DELETE' }),

  // ── Claims ─────────────────────────────────────────────────────────────
  claimTopics: () => request<TopicUseList>('/topics/claims'),
  atoms: (topic = '', limit = 50, command = '') =>
    request<AtomList>(withQuery('/atoms', { topic, limit, command })),
  atom: (id: string) => request<Atom>(`/atoms/${enc(id)}`),
  atomRelated: (id: string, limit = 20) =>
    request<AtomRelated>(withQuery(`/atoms/${enc(id)}/related`, { limit })),

  // ── Ask ────────────────────────────────────────────────────────────────
  asks: () => request<AskList>('/asks'),
  ask: (id: string) => request<SavedAsk>(`/asks/${enc(id)}`),
  deleteAsk: (id: string) => request<{ id: string }>(`/asks/${enc(id)}`, { method: 'DELETE' }),
  query: (question: string, source = '') =>
    request<DistillResponse>('/query', json('POST', { question, ...(source ? { source } : {}) })),
  run: (id: string) => request<Run>(`/runs/${enc(id)}`),

  // ── Assistant ──────────────────────────────────────────────────────────
  commands: (limit = 20) => request<CommandList>(withQuery('/commands', { limit })),
  command: (id: string) => request<Command>(`/commands/${enc(id)}`),
  runCommand: (text: string, context?: CommandContext) =>
    request<Command>('/commands', json('POST', context ? { text, context } : { text })),
  deleteCommand: (id: string) => request<{ id: string }>(`/commands/${enc(id)}`, { method: 'DELETE' }),

  // ── Search ─────────────────────────────────────────────────────────────
  search: (q: string, layer: 'atoms' | 'sources' = 'atoms', limit = 30, signal?: AbortSignal) =>
    request<SearchResult>(withQuery('/search', { q, layer, limit }), { signal }),

  // ── System ─────────────────────────────────────────────────────────────
  findings: (kind = '') => request<FindingList>(withQuery('/lint/findings', { kind })),
  lint: () => request<DistillResponse>('/lint', { method: 'POST' }),
  cleanup: () => request<CleanupResult>('/cleanup', { method: 'POST' }),
  reindex: () => request<ReindexResult>('/reindex', { method: 'POST' }),
  rebuildTrees: () =>
    request<{ sources: number; built: number; failed: number }>('/rebuild-trees', { method: 'POST' }),
  reconcile: () => request<ReconcileResult>('/reconcile', { method: 'POST' }),
  cluster: () => request<ClusterResult>('/cluster', { method: 'POST' }),

  vocabulary: () => request<{ namespaces: VocabularyHealth[] }>('/vocabulary'),
  curateVocabulary: () =>
    request<{ run_id: string; status: string }>('/vocabulary/curate', { method: 'POST' }),
  retopicClaims: () =>
    request<{ run_id: string; status: string }>('/vocabulary/retopic', { method: 'POST' }),

  atomLayout: () => request<AtomLayout>('/atoms/layout'),
  reorganizeAtoms: () => request<AtomLayout>('/atoms/reorganize', { method: 'POST' }),
  atomDuplicates: () => request<AtomDuplicates>('/atoms/duplicates'),
  collapseAtoms: () => request<AtomDuplicates>('/atoms/collapse', { method: 'POST' }),
  quoteClaimCode: () => request<ClaimCode>('/atoms/quote-code', { method: 'POST' }),
};

export type { Status, Pipeline, Source, Atom, WikiPage, Run, SavedAsk };
