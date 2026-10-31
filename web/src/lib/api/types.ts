// Wire shapes of the Thought Keeper HTTP API.
//
// These are the only types that mirror `internal/api/payloads`. The rest of the UI speaks in
// domain-level names (`source`, `claim`, `wiki page`) and never invents a second wire model.

export type StepName = 'capture' | 'outline' | 'distill' | 'cluster';
export type StepState = 'pending' | 'running' | 'ok' | 'failed' | 'skipped' | 'stale';

export interface StepStatus {
  state: StepState;
  at?: string;
  detail?: string;
  error?: string;
}

export interface Status {
  version: string;
  app: {
    addr: string;
    pid: number;
    headless: boolean;
    inbox?: { watched: boolean; error?: string };
    started_at: string;
    uptime: string;
  };
  vault: {
    root: string;
    sources: string;
    inbox: string;
    atoms: string;
    clusters: string;
    writable: boolean;
  };
  index: {
    path: string;
    open: boolean;
    outdated?: string;
    fts5: boolean;
    search: string;
    sources: number;
    /** Vault-wide badges, not counts of the loaded page of the source list. */
    bookmarks: number;
    failed: number;
    findings: number;
    atoms: number;
    clusters: number;
    unclustered: number;
    runs: number;
  };
  tools: {
    pdf: boolean;
    ocr: boolean;
    text_tool: string;
    rasterizer: string;
    tesseract: string;
  };
  agent: { configured: boolean; model: string; base_url?: string };
  /** The language of what the app writes: the UI default, the claims and the source overviews. */
  language: { ui: string; claims: string; summary: string };
  memory?: {
    heap_mb: number;
    heap_peak_mb: number;
    sys_mb: number;
    num_gc: number;
    peak_work?: string;
    peak_at?: string;
  };
  /** The source-overview backfill: measured backlog plus the summary worker's live state. */
  summary?: {
    missing: number;
    queued: number;
    running: number;
    /** When the backlog was last measured (RFC3339). */
    at?: string;
  };
  runs?: {
    pipeline_queued: number;
    pipeline_running: number;
    pipeline_workers: number;
    query_queued: number;
    lint_queued: number;
    lint_running: number;
  };
  pipeline?: Pipeline;
  message?: string;
}

export interface Pipeline {
  active: 'idle' | 'documents' | 'clusters' | string;
  running: Record<string, number>;
  queued: Record<string, number>;
  asking: number;
  jobs?: Job[];
}

export interface Job {
  step: string;
  state: 'running' | 'queued' | 'waiting' | 'pending' | string;
  why?: string;
  target?: string;
  title?: string;
  detail?: string;
  since?: string;
}

export interface Source {
  hash: string;
  kind: string;
  ext: string;
  path: string;
  url?: string;
  title: string;
  language?: string;
  origin?: string;
  wiki_page?: string;
  parent_hash?: string;
  sitemap?: boolean;
  // rss marks a feed: its snapshot is the item list and its children are the pages the items point at.
  rss?: boolean;
  // from_feed marks a snapshot taken from the feed's own copy of the post (the page may need
  // JavaScript); feed_hash is the feed it came from.
  from_feed?: boolean;
  feed_hash?: string;
  // wall is the last wall the page answered with (login_required, paywalled, consent_required).
  // Readable bytes can survive a later wall; the source page offers a manual Refresh for it.
  wall?: string;
  /** A snapshot the human supplied (a login, a paywall, a JavaScript page). */
  attached?: boolean;
  /** The file the snapshot came from; the receipt, not a path. */
  attached_from?: string;
  bytes: number;
  pages?: number;
  tree_state: string;
  original_name: string;
  /** The human's coarse labels, observed by the source-tags vocabulary. */
  tags?: string[];
  /** The agent's derived labels (kept apart from the human's), and when they were derived. */
  auto_tags?: string[];
  auto_tags_at?: string;
  parts_total: number;
  parts_done: number;
  atoms_left: number;
  atom_count: number;
  provenance_rows: number;
  running_step?: string;
  url_status?: number;
  url_checked_at?: string;
  bookmark?: boolean;
  imported_at: string;
  steps?: Partial<Record<StepName, StepStatus>>;
  sidecars?: Sidecar[];
  /** How many sub-sources this source owns (a sitemap's pages, a wiki page's linked URLs). */
  children?: number;
}

export interface Sidecar {
  name: string;
  bytes: number;
  object?: boolean;
}

export interface SourceList {
  sources: Source[];
  /** The TOTAL the list holds, not the page length: more exist past this page. */
  count: number;
  total: number;
  /** Set when the list is an assistant result (see CommandRef). */
  command?: CommandRef;
}

/** SourceListFilter is the server-side narrowing of the Sources window. Empty fields mean "all". */
export interface SourceListFilter {
  kind?: string;
  query?: string;
  tag?: string;
  attachedOnly?: boolean;
  bookmarksOnly?: boolean;
  failedOnly?: boolean;
  /**
   * command is an assistant result: the server reads the materialized set from the record instead
   * of filtering. It is one filter dimension, so the window (count, pages, scroll height) works
   * exactly like a tag filter.
   */
  command?: string;
}

export interface SourceTagFacet {
  tag: string;
  count: number;
}

/** SourceFacets is the vault-wide tag cloud and the attached-snapshot count, not window facts. */
export interface SourceFacets {
  tags: SourceTagFacet[];
  attached: number;
}

export interface SourceNode {
  node_id: string;
  parent_id: string;
  depth: number;
  ord: number;
  title: string;
  summary: string;
  start_page?: number;
  end_page?: number;
  ocr?: boolean;
  text?: string;
  has_text?: boolean;
}

export interface SourceTree {
  hash: string;
  nodes: SourceNode[];
}

export interface SourceTopic {
  name: string;
  count: number;
}

export interface TopicUse {
  topic: string;
  count: number;
}

export interface TopicUseList {
  topics: TopicUse[];
  count: number;
}

export interface AtomSource {
  hash: string;
  title?: string;
  node?: string;
  ocr?: boolean;
}

export interface Atom {
  id: string;
  topic: string;
  path?: string;
  claim: string;
  body: string;
  type: string;
  status: string;
  sources?: AtomSource[];
  source_count: number;
  clusters?: string[];
  cluster_count?: number;
  created_at?: string;
  updated_at: string;
}

/** AtomRelated is the two relations of one claim, kept apart: the claims it travels with in a
 *  cluster (peers) and the claims that merely share its flat topic. */
export interface AtomRelated {
  peers: Atom[];
  same_topic: Atom[];
  count: number;
}

export interface AtomList {
  atoms: Atom[];
  count: number;
  total?: number;
  topics?: SourceTopic[];
  /** Set when the list is an assistant result. */
  command?: CommandRef;
}

export interface Branch {
  topic: string;
  count: number;
}

export interface WikiSummary {
  id: string;
  title: string;
  tags?: string[];
  auto_tags?: string[];
  source_hash?: string;
  updated_at?: string;
}

export interface WikiTag {
  tag: string;
  count: number;
}

export interface WikiList {
  pages: WikiSummary[];
  tags: WikiTag[];
  count: number;
}

export interface WikiPage extends WikiSummary {
  urls?: string[];
  body: string;
  created_at?: string;
}

export interface AskSummary {
  id: string;
  question: string;
  tags?: string[];
  /** The hash of the one source this question was scoped to; absent for a vault-wide answer. */
  source?: string;
  created_at: string;
  updated_at: string;
}

export interface SavedAsk extends AskSummary {
  answer: string;
}

export interface AskList {
  asks: AskSummary[];
  count: number;
}

export interface RunStep {
  seq: number;
  tool: string;
  is_error: boolean;
  duration_ms: number;
}

export interface Run {
  id: string;
  op: string;
  model: string;
  target_id?: string;
  status: 'running' | 'succeeded' | 'failed' | 'aborted' | string;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens: number;
  iterations: number;
  question?: string;
  answer: string;
  error: string;
  started_at?: string;
  ended_at?: string;
  steps?: RunStep[];
}

export interface Finding {
  id: string;
  run_id?: string;
  layer: string;
  kind: string;
  severity: 'info' | 'warning' | 'error' | string;
  subject_kind: string;
  subject_id?: string;
  message: string;
}

export interface FindingList {
  findings: Finding[];
  count: number;
}

export interface VocabularyNameUses {
  name: string;
  uses: number;
}

export interface VocabularyGroup {
  reason: 'same-key' | 'shared-words' | string;
  names: VocabularyNameUses[];
}

export interface VocabularyHealth {
  namespace: string;
  names: number;
  uses: number;
  singletons: number;
  singleton_share: number;
  coverage: number;
  collisions?: VocabularyGroup[];
  near_duplicates?: VocabularyGroup[];
  collision_count?: number;
  near_duplicate_count?: number;
  broad_tokens?: number;
  broad_tokens_learned?: boolean;
  oversized_families?: number;
  oversized_names?: number;
  canonical: number;
  absorbed: number;
  new?: string[];
  top?: VocabularyNameUses[];
}

export interface AtomDuplicates {
  groups: number;
  files: number;
  collapsed: number;
  example?: string;
}

/** The one-time repair that wraps the bracket syntax in claim bodies as inline code. */
export interface ClaimCode {
  scanned: number;
  repaired: number;
  fragments: number;
  skipped: number;
}

export interface AtomLayout {
  folders?: string[];
  claims: number;
  indexes?: number;
  moved: number;
  removed: number;
  folders_removed?: number;
  indexes_removed?: number;
  skipped?: number;
  kept?: string[];
}

export interface SearchHit {
  id: string;
  kind: string;
  title: string;
  snippet: string;
}

export interface SearchResult {
  hits: SearchHit[];
  count: number;
}

export interface DistillResponse {
  run_id: string;
  status: string;
}

export interface CaptureResult {
  queued: boolean;
  detail?: string;
}

export interface ClusterResult {
  queued: boolean;
  run_id?: string;
}

export interface ReconcileResult {
  checked: number;
  invalidated?: string[];
  broken?: string[];
  languages?: number;
  identities?: number;
  // A row that had lost its owner (origin/wiki_page) and got it back from the source's record.
  ownerships?: number;
}

export interface ReindexResult {
  scanned: number;
  indexed: number;
  added: number;
  removed: number;
  atoms_removed?: number;
  atoms_orphaned?: number;
  provenance_pruned?: number;
  clusters: number;
  clusters_removed?: number;
  atoms: number;
  steps?: number;
  parts_pruned?: number;
  skipped: number;
  orphans: number;
}

export interface DeleteResult {
  hash: string;
  atoms_removed: number;
  dependents_removed?: number;
}

/** The result of trying the URL again for an attached snapshot. */
export interface RefetchResult {
  /** The page was readable: the attachment was replaced by a regular snapshot. */
  replaced: boolean;
  /** The page is still behind a wall; the attached snapshot was kept. */
  blocked: boolean;
  /** The fetch failed; the attached snapshot was kept. */
  failed: boolean;
  detail?: string;
}

export interface RefreshResult {
  checked: number;
  changed: number;
  failed: number;
  // A page that answered with a wall (a login, a paywall) is not a failure: the site is fine, it just
  // did not answer with itself.
  blocked?: number;
  // Sources with a human-supplied snapshot; they are not fetched at all.
  attached?: number;
  // New pages a sitemap or a feed declared in this pass, and pages a sitemap no longer lists.
  discovered?: number;
  removed?: number;
  // Why a ONE-SOURCE refresh failed (the per-source Refresh button).
  detail?: string;
}

export interface SourceReset {
  hash: string;
  atoms: number;
  nodes: number;
  pages: number;
  bookmark?: boolean;
}

export interface RebuildTreeResult {
  hash: string;
  nodes: number;
  pages: number;
  title: string;
  ocr: boolean;
}

export interface CleanupResult {
  clusters_pruned: number;
  clusters_removed: number;
  findings: number;
}

export interface RawResponse {
  text: string;
  contentType: string;
}

// ── Events ────────────────────────────────────────────────────────────────

export type EventType =
  | 'run'
  | 'source'
  | 'wiki'
  | 'clusters'
  | 'findings'
  | 'urls'
  | 'pipeline'
  | 'progress'
  | 'log'
  | 'vocabulary'
  | 'command';

export interface StreamFrame {
  type: EventType | string;
  data?: Record<string, unknown>;
  at?: string;
}

export interface ProgressFrame {
  hash: string;
  phase?: string;
  msg?: string;
  fields: Record<string, number>;
}

// ── Assistant ─────────────────────────────────────────────────────────────

/** CommandCriteria is what a command MEANT: the reading the banner shows. */
export interface CommandCriteria {
  text?: string;
  tag?: string;
  kind?: string;
  topic?: string;
  /** A topic read as PROVENANCE: the sources that own claims with it. */
  from_topic?: string;
  /** The vault's own names the words were mapped onto. */
  tags?: string[];
  topics?: string[];
  /** The source the reader pointed at (or pasted). */
  source?: string;
  /** The claim the reader pointed at (or pasted). */
  atom?: string;
  since?: string;
  state?: string;
  order?: string;
  bookmarks_only?: boolean;
  attached_only?: boolean;
}

export interface CommandName {
  name: string;
  kind: 'tag' | 'topic';
  /** How often the vault uses the name. */
  count: number;
  /** How many items of THIS set the name produced. */
  found: number;
}

export interface CommandItem {
  ref: string;
  title?: string;
  kind?: string;
  /** The model's reason for putting THIS item in a researched set. */
  why?: string;
}

/**
 * Command is one "request → GUI state" record. It is short-lived: the server prunes the history, so
 * an address that names one has an expiry date.
 */
export interface Command {
  id: string;
  text: string;
  lens: string;
  url: string;
  /** "filter" (deterministic), "research" (a model curated it) or "ask" (delegated question). */
  resolution?: string;
  /** Why THIS set: the assistant's one-sentence answer to the request. */
  rationale?: string;
  /** Honest footnotes: a criterion read differently, a reference that vanished, a cut set. */
  notes?: string[];
  /** The vocabulary names the request was mapped onto, with what each contributed. */
  mapped?: CommandName[];
  total: number;
  vault?: number;
  scanned?: number;
  generated?: string;
  /** End-to-end time of the request, in milliseconds. */
  took_ms?: number;
  created_at: string;
  criteria: CommandCriteria;
  items?: CommandItem[];
}

/** CommandContext is the surface a request was made from: "this claim" needs to know which one. */
export interface CommandContext {
  view?: string;
  id?: string;
}

export interface CommandList {
  commands: Command[];
  count: number;
}

/** CommandRef is what a list says about the command it is showing. */
export interface CommandRef {
  id: string;
  expired?: boolean;
  total: number;
  missing?: number;
}
