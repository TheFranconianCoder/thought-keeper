<script lang="ts">
	import { onMount, tick } from 'svelte';
	import Sidebar from './components/Sidebar.svelte';
	import Claims from './components/Claims.svelte';
	import Wiki from './components/Wiki.svelte';
	import Attention from './components/Attention.svelte';
	import Ask from './components/Ask.svelte';
	import Sources from './components/Sources.svelte';
	import Bookmarks from './components/Bookmarks.svelte';
	import Status from './components/Status.svelte';
	import Icon from './components/Icon.svelte';
	import AddSourceDialog from './components/AddSourceDialog.svelte';
	import SearchPalette from './components/SearchPalette.svelte';
	import SourceViewer from './components/SourceViewer.svelte';
	import ConfirmDialog from './components/ConfirmDialog.svelte';
	import { app } from './lib/state.svelte';
	import { connectEvents } from './lib/sse';
	import { pipelineLabel } from './lib/sourceStatus';
	import { documentTitle } from './lib/shell.svelte';
	import { parseHash } from './lib/nav';
	import { remember, restoreNow } from './lib/scrollMemory';
	import { claimWindow, focusPrimary } from './lib/singleWindow';
	import {
		enqueueEvent,
		loadFindings,
		loadAttention,
		loadSources,
		loadPipeline,
		loadStatus,
		openClaim,
		openSearch,
		openAsk,
		loadAsks,
		loadClaims,
		loadClaimTopics,
		loadWiki,
		openWiki,
		loadSource,
		refreshView,
		resyncAll,
	} from './lib/actions';

	const THEMES = ['auto', 'dark', 'light'] as const;
	type Theme = (typeof THEMES)[number];

	let theme = $state<Theme>((localStorage.getItem('tk-theme') as Theme) ?? 'auto');

	$effect(() => {
		document.documentElement.dataset.theme = theme;
		localStorage.setItem('tk-theme', theme);
	});

	function cycleTheme(): void {
		theme = THEMES[(THEMES.indexOf(theme) + 1) % THEMES.length];
	}

	/** applyOverlay turns the URL into overlay state. It is the ONE place that does: the overlays and
	 *  their tabs are part of the address, so back/forward walks the UI state and a link can be shared. */
	function applyOverlay(params: URLSearchParams): void {
		app.ui.search = params.get('search') === '1';
		app.ui.add = params.get('add') === '1';
		app.ui.viewerTab = params.get('tab') ?? '';
		app.ui.viewerPart = params.get('part') ?? '';
		app.ui.viewerFile = params.get('file') ?? '';
	}

	function route(): void {
		const { view, id, params } = parseHash(location.hash);
		applyOverlay(params);
		if (view === 'claims') {
			app.view = 'claims';
			const topic = params.get('topic') ?? '';
			if (id) {
				app.claims.topic = '';
				void openClaim(id);
			} else {
				app.claims.selected = null;
				void loadClaims(topic);
				void loadClaimTopics();
			}
		} else if (view === 'ask') {
			app.view = 'ask';
			if (id) {
				void openAsk(id);
			} else {
				app.asks.selected = null;
				app.asks.selectedId = '';
				void loadAsks();
			}
		} else if (view === 'sources') {
			if (id) {
				app.view = 'source';
				if (app.ui.viewer?.hash !== id) {
					app.ui.viewer = null;
					void loadSource(id);
				}
			} else {
				app.view = 'sources';
				app.ui.viewer = null;
				void loadSources();
			}
		} else if (view === 'wiki') {
			app.view = 'wiki';
			if (id === 'new') {
				app.wiki.newPage = true;
				app.wiki.selected = null;
				app.wiki.selectedId = '';
				void loadWiki();
			} else if (id) {
				app.wiki.newPage = false;
				void openWiki(id);
			} else {
				app.wiki.newPage = false;
				app.wiki.selected = null;
				app.wiki.selectedId = '';
				void loadWiki();
			}
		} else if (view === 'attention') {
			app.view = 'attention';
			void loadAttention();
		} else if (view === 'bookmarks') {
			app.view = 'bookmarks';
			void loadSources();
		} else if (view === 'status') {
			app.view = 'status';
			void loadFindings();
		} else {
			app.view = 'claims';
			void loadClaims('');
			void loadClaimTopics();
		}
	}



	// What the app is doing RIGHT NOW, globally (documents or the cluster step — the two working states of
	// the strictly separated pipeline), plus the queries that run beside everything else.
	const pipelineText = $derived(
		[ pipelineLabel(app.pipeline), (app.pipeline?.asking ?? 0) > 0 ? 'asking…' : '' ]
			.filter(Boolean)
			.join(' · '),
	);

	// The bar says WHERE you are.
	const crumb = $derived.by(() => {
		if (app.view === 'claims' && app.claims.selected) {
			return { topic: app.claims.selected.topic, title: '' };
		}
		if (app.view === 'source' && app.ui.viewer) {
			return { topic: 'Sources', title: app.ui.viewer.title };
		}
		if (app.view === 'wiki' && app.wiki.selected) {
			return { topic: 'Wiki', title: app.wiki.selected.title };
		}
		return { topic: '', title: { claims: 'Claims', wiki: 'Wiki', ask: 'Ask', sources: 'Sources', source: 'Sources', bookmarks: 'Bookmarks', attention: 'Attention', status: 'Status' }[app.view] };
	});

	// The tab (and the window list) still names the page: there the title is the only thing that says
	// which document this is — the bar above it is not visible in a tab.
	const tabTitle = $derived(app.claims.selected?.claim || app.ui.viewer?.title || app.wiki.selected?.title || crumb.title || 'Thought Keeper');

	// Every view starts at the TOP of itself. A scroll position belongs to what is being read, so
	// carrying it over drops the reader into the middle of something they have not seen — and on a
	// long page it looks like nothing changed at all.
	//
	// The key is the ROUTE, not just the open page: switching from Claims to Status used to keep the
	// scroll (the view changed, the scroll did not), and so did a change of topic or tag filter.
	let scroller = $state<HTMLElement | null>(null);
	const routeKey = $derived(
		[
			app.view,
			app.claims.selected?.id ?? '',
			app.claims.topic,
			app.asks.selectedId,
			app.view === 'source' ? app.ui.viewer?.hash ?? '' : '',
			app.view === 'wiki' ? (app.wiki.selectedId || (app.wiki.newPage ? 'new' : '')) : '',
		].join('|'),
	);
	$effect(() => {
		void routeKey;
		const s = scroller;
		if (!s) return;
		// After the render: the new view is in the DOM when this runs. It does NOT jump to the top —
		// restoreNow reads the position THIS history entry had, and only a new entry starts at the top.
		void tick().then(() => restoreNow());
	});

	// The tab (and the OS window title where the platform has one) says what you are looking at —
	// and what the app is doing while it does something.
	$effect(() => {
		// Where the platform has no overlay (macOS, a browser tab), this IS the window/tab title;
		// where it has one, the title bar carries the page and the tab carries the activity.
		document.title = documentTitle(tabTitle, app.banner?.op ?? null);
	});

	onMount(() => {
		// The stream is connected BEFORE the first loads: an event that arrives while they are
		// in flight is then never missed, and the guards make sure a slow response cannot win
		// against it.
		const disconnect = connectEvents(
			(ev) => enqueueEvent(ev.type, ev.data ?? {}),
			() => void resyncAll()
		);
		void (async () => {
			// The global pipeline status is loaded with the rest, so the title bar is right from
			// the first frame (and the SSE `pipeline` events keep it live afterwards).
			await Promise.all([loadStatus(), loadPipeline()]);
			await Promise.all([loadSources(), loadFindings(), loadWiki()]);
			route();
		})();
		window.addEventListener('hashchange', route);
		const onShortcut = (e: KeyboardEvent): void => {
			if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
				e.preventDefault();
				openSearch();
			}
		};
		window.addEventListener('keydown', onShortcut);
		// The scroll position is part of the history entry: a reader who comes back lands where they
		// were, instead of at the top of a view they had already read.
		if (scroller) remember(scroller);
		// The UI owns the single-window rule: a duplicate closes/focuses itself.
		claimWindow(() => {
			app.ui.duplicate = true;
			window.close(); // may be refused for a non-script-opened window
		});
		return () => {
			window.removeEventListener('hashchange', route);
			window.removeEventListener('keydown', onShortcut);
			disconnect();
		};
	});

</script>

{#snippet topbar()}
		<div class="topbar" role="presentation">
			<div class="crumb">
				<!-- Without a title the topic IS the current location, and reads like one. -->
				{#if crumb.topic}<span class={crumb.title ? '' : 'cur'}>{crumb.topic}</span>{/if}
				{#if crumb.topic && crumb.title}<span class="sep">/</span>{/if}
				{#if crumb.title}<span class="cur">{crumb.title}</span>{/if}
			</div>
			<span class="spacer"></span>
			<!-- The GLOBAL pipeline status: documents (capture/outline/distill, per source) or the
			     cluster step (over everything) — never both, because that is the rule. Ask is always
			     allowed and is not gated, so it shows on its own. -->
			{#if pipelineText}
				<span class="run-chip" title="The four steps are strictly separated: the cluster step only runs while no document step runs">
					<span class="spin"></span>{pipelineText}
				</span>
			{:else if app.banner}
				<span class="run-chip"><span class="spin"></span>{app.banner.op}…</span>
			{/if}
			<button
				class="icon-btn"
				title={theme === 'auto' ? 'Theme: follows the system (auto)' : `Theme: ${theme}`}
				onclick={cycleTheme}
			>
				<Icon name={theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'auto'} />
			</button>
		</div>
{/snippet}

<div class="app">
	<Sidebar />

	<main class="main">
		{@render topbar()}
		<!-- The bar's space is always reserved, so showing it cannot shift the page. -->
		<div class="progress" class:on={!!app.banner}><i></i></div>

		<!-- Not connected: said HERE, in the content area, and not only as a dot in the sidebar's
		     footer. An app that cannot reach its backend shows stale or empty views, and "there are no
		     claims" is a claim — the reader has to be told that the connection is what is missing, and
		     what to do about it. -->
		{#if app.connection !== 'live'}
			<div class="offline-strip" role="status">
				<span class="dot {app.connection === 'reconnecting' ? 'dot-warn' : 'dot-off'}"></span>
				<span>
					<b>Not connected to Thought Keeper.</b>
					The app is not running, or the connection dropped — what you see may be out of date.
					Start it (the tray icon) and this page reconnects by itself.
				</span>
			</div>
		{/if}

		<div class="scroll" bind:this={scroller}>
			{#if app.view === 'claims'}
				<Claims />
			{:else if app.view === 'wiki'}
				<Wiki />
			{:else if app.view === 'ask'}
				<Ask />
			{:else if app.view === 'sources'}
				<Sources />
			{:else if app.view === 'source'}
				<SourceViewer />
			{:else if app.view === 'attention'}
				<Attention />
			{:else if app.view === 'bookmarks'}
				<Bookmarks />
			{:else}
				<Status />
			{/if}
		</div>
	</main>
</div>

<AddSourceDialog />
	<SearchPalette />
	<ConfirmDialog />

{#if app.ui.duplicate}
	<div class="overlay">
		<div class="dialog" role="dialog" aria-label="Already open" tabindex="-1">
			<h2>Thought Keeper is already open</h2>
			<p class="sub">Only one window of the app is supported. Another window is already running.</p>
			<div class="dialog-foot">
				<button
					class="btn btn-accent"
					onclick={() => {
						focusPrimary();
						window.close();
					}}>Use the other window</button
				>
				<button
					class="btn"
					onclick={() => (app.ui.duplicate = false)}>Use this window</button
				>
			</div>
		</div>
	</div>
{/if}

{#if app.ui.toast}
	<div class="toast">{app.ui.toast}</div>
{/if}
