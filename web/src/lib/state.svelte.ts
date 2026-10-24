import type {
	Atom,
	AskSummary,
	Branch,
	AtomDuplicates,
	AtomLayout,
	Finding,
	Pipeline,
	SearchHit,
	SavedAsk,
	Source,
	Status,
	VocabularyHealth,
	WikiList,
	WikiPage,
} from './types';

export type View = 'claims' | 'wiki' | 'ask' | 'sources' | 'source' | 'bookmarks' | 'attention' | 'status';

export interface Banner {
	op: string;
	status: string;
}

/** app is the single reactive store (Svelte 5 runes deep-proxy it). */
/** ConfirmRequest is one question the app asks in its own dialog. */
export type ConfirmRequest = {
	title: string;
	message?: string;
	confirmLabel?: string;
	danger?: boolean;
	onConfirm: () => void | Promise<void>;
};

export const app = $state({
	view: 'claims' as View,
	status: null as Status | null,
	// The GLOBAL pipeline status: which of the four separated steps is running (documents or the
	// cluster step) and how many are queued. The title bar shows it; the steps themselves are strictly
	// separated, so this is a single answer, not a list.
	pipeline: null as Pipeline | null,
	connection: 'connecting' as 'connecting' | 'live' | 'reconnecting',
	banner: null as Banner | null,

	ui: {
		search: false,
		add: false,
		toast: null as string | null,
		viewer: null as { hash: string; title: string; kind: string; ext: string } | null,
		// The overlay's TAB and the open outline part / file: part of the ADDRESS (see nav.ts), so
		// back/forward walks the UI state. They live here because the route sets them and the viewer
		// reads them — the URL is the truth about which tab is open.
		viewerTab: '',
		viewerPart: '',
		viewerFile: '',
		// Bumped whenever sources/associations changed, so an OPEN source page reloads its
		// claims/meta instead of showing the state from the moment it was opened.
		sourceVersion: 0,
		// true when another window already holds the single-window claim
		duplicate: false,
		// An in-app question (never a native browser dialog).
		confirm: null as ConfirmRequest | null,
		// The open source's log, kept live by the SSE `log` events.
		sourceLog: null as { hash: string; text: string } | null,
	},

	wiki: {
		pages: [] as WikiList['pages'],
		tags: [] as WikiList['tags'],
		selected: null as WikiPage | null,
		selectedId: '',
		notice: '',
		loaded: false,
		newPage: false,
		tagFilter: '',
	},

	claims: {
		items: [] as Atom[],
		total: 0,
		topic: '',
		claimTopics: [] as Branch[],
		selected: null as Atom | null,
		related: [] as Atom[],
		notice: '',
		loaded: false,
	},

	sources: {
		// What a source is doing right now, fed by the SSE `progress` events: the counts
		// (parts done, claims, open) plus the phase and its message. The row and the detail
		// overlay read the same map — one source of truth for "is it working".
		status: {} as Record<string, Record<string, unknown>>,
		items: [] as Source[],
		notice: '',
	},

	ask: {
		runId: null as string | null,
		answer: '',
		question: '',
		notice: '',
		busy: false,
	},

	attention: {
		loaded: false,
		notice: '',
	},

	asks: {
		items: [] as AskSummary[],
		selected: null as SavedAsk | null,
		selectedId: '',
		currentId: '',
		notice: '',
		loaded: false,
	},

	lint: {
		findings: [] as Finding[],
		notice: '',
	},

	// The claim layout of the vault: what still sits in the old legacy topic folders, if anything. Read on
	// the Status page, where the button that moves it lives.
	vault: {
		atomLayout: null as AtomLayout | null,
		atomDuplicates: null as AtomDuplicates | null,
		vocabulary: null as VocabularyHealth[] | null,
	},

	search: {
		q: '',
		hits: [] as SearchHit[],
		active: 0,
		busy: false,
	},
});
