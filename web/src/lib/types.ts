// Wire shapes mirroring internal/api/payloads. Hand-kept in sync with the Go side.

export interface Status {
	version: string;
	app: {
		addr: string;
		pid: number;
		headless: boolean;
		started_at: string;
		uptime: string;
		/** The drop zone: watched (inotify) or only polled, with the reason when it is polled. */
		inbox?: { watched: boolean; error?: string };
	};
	vault: { root: string; sources: string; inbox: string; atoms: string; clusters: string; writable: boolean };
	index: {
		path: string;
		open: boolean;
		/** The sentence for an index written by a build with another SHAPE (empty when it is the one
		 *  this build writes, or when it is new). While it is set, every read and write fails. */
		outdated?: string;
		fts5: boolean;
		search: string;
		sources: number;
		atoms: number;
		/** the association layer and the claims it does not own yet. */
		clusters: number;
		unclustered: number;
		runs: number;
	};
	tools: { pdf: boolean; ocr: boolean; text_tool: string; rasterizer: string; tesseract: string };
	agent: { configured: boolean; model: string; base_url?: string };
}

export interface Source {
	hash: string;
	kind: string;
	ext: string;
	path: string;
	url?: string;
	title: string;
	/** detected source language (ISO code), a search hint */
	language?: string;
	/** set by a wiki page: origin "wiki" and the page id */
	origin?: string;
	wiki_page?: string;
	bytes: number;
	pages?: number;
	tree_state: string;
	original_name: string;
	parts_total: number;
	parts_done: number;
	atoms_left: number;
	atom_count: number;
	provenance_rows: number;
	/** The DOCUMENT step running for this source right now ("capture"/"outline"/"distill"), or "". */
	running_step?: string;
	url_status?: number;
	url_checked_at?: string;
	bookmark?: boolean;
	imported_at: string;
	/** The four pipeline steps' state: capture → outline → distill → cluster. */
	steps?: Record<string, StepStatus>;
	/** The source's files (the object and its sidecars) — only on the single-source read. */
	sidecars?: Sidecar[];
}

/** StepStatus is one step's light for one source: pending (open circle), running (blue),
 *  ok (green), failed (red), skipped (grey), stale (amber). */
/** WikiSummary is one user-authored page in the wiki list. */
export interface WikiSummary {
	id: string;
	title: string;
	tags?: string[];
	/** auto_tags are derived from the page's own title and prose; the human's tags are `tags`. */
	auto_tags?: string[];
	source_hash?: string;
	updated_at?: string;
}

/** WikiTag is one tag of the cloud. */
export interface WikiTag {
	tag: string;
	count: number;
}

export interface WikiList {
	pages: WikiSummary[];
	tags: WikiTag[];
	count: number;
}

/** WikiPage is one page including its Markdown body. */
export interface WikiPage {
	id: string;
	title: string;
	tags?: string[];
	/** auto_tags are derived from the page's own title and prose; the human's tags are `tags`. */
	auto_tags?: string[];
	/** external URLs tracked as their own sources */
	urls?: string[];
	body: string;
	source_hash?: string;
	created_at?: string;
	updated_at?: string;
}

export interface StepStatus {
	state: 'pending' | 'running' | 'ok' | 'failed' | 'skipped' | 'stale' | string;
	at?: string;
	detail?: string;
	error?: string;
}

/** Pipeline is the GLOBAL status: which of the four strictly separated steps is running. The
 *  cluster step never runs while a document step does, so "documents" and "clusters" cannot both be
 *  active. */
/** Job is one piece of work the app holds right now: which step, what it is about, and whether it is
 *  executing or waiting. The counts say how busy the app is; this says what it is busy WITH. */
export interface Job {
	step: string;
	state: 'running' | 'queued' | 'waiting' | 'pending' | string;
	/** why a job is not running (the cluster pass holds the gate, other runs are ahead). */
	why?: string;
	/** the source hash; empty for a step that runs over the whole vault. */
	target?: string;
	title?: string;
	/** what a maintenance job is doing right now ("claim topics · asking the model") */
	detail?: string;
	since?: string;
}

export interface Pipeline {
	active: 'idle' | 'documents' | 'clusters' | string;
	running: Record<string, number>;
	queued: Record<string, number>;
	asking: number;
	jobs?: Job[];
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
	/** has_text marks a part with text of its own: those can be read, the others are grouping
	 *  headings (a document title, a part). */
	has_text?: boolean;
}

export interface Atom {
	id: string;
	topic: string;
	claim: string;
	body: string;
	type: string;
	status: string;
	/** Where the claim came from: the source nodes it was distilled from. */
	sources?: AtomSource[];
	source_count: number;
	/** The associations this claim belongs to. A cluster has no title and is never listed: the
	 *  Claims detail view uses these to show the claim's peers. */
	clusters?: string[];
	cluster_count?: number;
	updated_at: string;
}

export interface AtomSource {
	hash: string;
	title?: string;
	node?: string;
	ocr?: boolean;
}

/** VocabularyNameUses is one name of a vocabulary and how often it is used. */
export interface VocabularyNameUses {
	name: string;
	uses: number;
}

/** VocabularyGroup is a set of names the numbers say belong together — a CANDIDATE, not a decision. */
export interface VocabularyGroup {
	reason: 'same-key' | 'shared-words' | string;
	names: VocabularyNameUses[];
}

/** VocabularyHealth is one namespace's numbers: observed (the index) against decided (the vault). */
export interface VocabularyHealth {
	namespace: string;
	names: number;
	uses: number;
	/** the long tail: names used exactly once */
	singletons: number;
	singleton_share: number;
	/** the share of all uses the biggest names carry — does the head do the work? */
	coverage: number;
	collisions?: VocabularyGroup[];
	near_duplicates?: VocabularyGroup[];
	/** the FULL counts (the lists above are capped so the report stays small) */
	collision_count?: number;
	near_duplicate_count?: number;
	/** learned tokens ignored by near-duplicate detection, and families too broad to merge */
	broad_tokens?: number;
	broad_tokens_learned?: boolean;
	oversized_families?: number;
	oversized_names?: number;
	canonical: number;
	absorbed: number;
	/** observed names the vocabulary has no opinion about yet */
	new?: string[];
	top?: VocabularyNameUses[];
}

export interface Branch {
	topic: string;
	count: number;
}

export interface SearchHit {
	id: string;
	kind: string;
	title: string;
	snippet: string;
}

export interface Finding {
	id: string;
	run_id?: string;
	layer: string;
	kind: string;
	severity: string;
	subject_kind: string;
	subject_id?: string;
	message: string;
}

export interface RunStep {
	seq: number;
	tool: string;
	is_error: boolean;
	duration_ms: number;
}

export interface Run {
	/** question is what a QUERY run was asked (empty for other runs). */
	question?: string;
	id: string;
	op: string;
	model: string;
	status: string;
	total_tokens: number;
	iterations: number;
	answer: string;
	error: string;
	steps?: RunStep[];
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
}

export interface Sidecar {
	name: string;
	/** -1 when the file is missing (the consistency job repairs that). */
	bytes: number;
	object?: boolean;
}

export interface DistillResponse {
	run_id: string;
	status: string;
}

/** AskSummary is one saved Ask in the list (the answer is read when it is opened). */
export interface AskSummary {
	id: string;
	question: string;
	created_at: string;
	updated_at: string;
}
export interface SavedAsk {
	id: string;
	question: string;
	answer: string;
	created_at: string;
	updated_at: string;
}
export interface AskList {
	asks: AskSummary[];
	count: number;
}

export interface SourceList {
	sources: Source[];
	count: number;
}
export interface AtomList {
	atoms: Atom[];
	count: number;
	/** total is how many claims the source really has: the list is bounded (a book has hundreds, and
	 *  shipping every body is what made opening the overlay slow). */
	total?: number;
	/** topics describe ALL of the source's claims, not just the returned slice, with their counts:
	 *  the rail shows the first few, which is what the source is actually about. */
	topics?: SourceTopic[];
}

/** SourceTopic is one of a source's claim topics and how many of its claims carry it. */
export interface SourceTopic {
	name: string;
	count: number;
}
export interface TopicUseList {
	topics: Branch[];
	count: number;
}
export interface SearchResult {
	hits: SearchHit[];
	count: number;
}
/** AtomLayout is what a vault still holds in the OLD claim layout (legacy topic folders) and what a
 *  reorganize did about it. */
export interface AtomLayout {
	folders?: string[];
	/** claims and indexes: what the folders hold — claims, and the generated legacy index files
	 *  beside them (they catalog a folder, so they go with it). */
	claims: number;
	indexes?: number;
	moved: number;
	folders_removed: number;
	indexes_removed?: number;
	dirs_removed?: number;
	skipped?: number;
	kept?: string[];
}

/** AtomDuplicates is what exists more than once: the same claim written several times (a write that
 *  could not be recorded left its file behind, and a model that retries leaves one file per attempt). */
export interface AtomDuplicates {
	groups: number;
	files: number;
	collapsed: number;
	example?: string;
}

export interface FindingList {
	findings: Finding[];
	count: number;
}
export interface RefreshResult {
	checked: number;
	changed: number;
	failed: number;
}
export interface DeleteResult {
	hash: string;
	atoms_removed: number;
}



export interface Conn {
	state: 'connecting' | 'live' | 'reconnecting';
}

export type View = 'library' | 'inbox' | 'ask' | 'health';
