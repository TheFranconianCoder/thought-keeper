import type { Pipeline, ProgressFrame, StreamFrame } from '../api/types';
import { router } from '../router.svelte';
import { EventStream } from './event-stream';
import { noteFrame, setConnectionState, connection } from '../stores/connection.svelte';
import { system } from '../stores/system.svelte';
import { sources } from '../stores/sources.svelte';
import { bookmarkWindow, failedWindow, sourceWindow } from '../stores/source-window.svelte';
import { claims } from '../stores/claims.svelte';
import { wiki } from '../stores/wiki.svelte';
import { asks } from '../stores/asks.svelte';
import { assistant } from '../stores/assistant.svelte';

/**
 * The event pipeline.
 *
 *  SSE frames are not handled one request at a time. They are folded into two lanes:
 *
 *  1. LIVE — progress counters and log lines. These carry their payload; they are merged per source
 *     and flushed once per animation-sized tick. A run that emits hundreds of log lines therefore
 *     causes a handful of reactive updates, not hundreds.
 *
 *  2. STATE — "this container changed". These are coalesced into a set of scopes over a short
 *     window. Each scope is a SINGLE container read, so a burst cannot load the same list twice; a
 *     batch that arrives while another is loading is picked up by the next turn. That is what keeps
 *     a burst of events from firing overlapping requests.
 *
 *  A scope is also VIEW-AWARE. A run publishes several events (source, clusters, run) for one
 *  source; the list, the claim cloud and the vault report only change on screen if the reader is
 *  looking at them. Hidden containers are skipped and — where a view does not reload on entry by
 *  itself — marked stale, so opening the view still shows the truth. One pipeline run therefore
 *  costs the visible lens a few reads, not every container in the app.
 */

type Scope =
  | 'sources' // the source list
  | 'source' // the open source's detail
  | 'claims' // the claim list
  | 'topics' // the topic cloud
  | 'claim' // the open claim
  | 'status' // the badges and the job queue
  | 'findings'
  | 'vault' // layout, duplicates and vocabulary health (Status)
  | 'vocabulary'
  | 'wiki'
  | 'asks';

/** How long state scopes wait so one run's source/clusters/run frames become one batch. */
const SCOPE_COALESCE_MS = 150;

const scopes = new Set<Scope>();
let scheduled = false;
let flushTimer: ReturnType<typeof setTimeout> | undefined;
let flushing = false;
let liveTimer: ReturnType<typeof setTimeout> | undefined;

const progressBuffer = new Map<string, ProgressFrame>();
const logBuffer = new Map<string, string>();

const viewIs = (...views: string[]): boolean => views.includes(router.current.view);
const claimsVisible = (): boolean => viewIs('claims');
const topicsVisible = (): boolean => viewIs('claims', 'ask');
const sourceDetailVisible = (): boolean => viewIs('source');
const findingsVisible = (): boolean => viewIs('attention', 'status');
const vaultVisible = (): boolean => viewIs('status');

function schedule(scope: Scope): void {
  scopes.add(scope);
  if (scheduled || flushing) return;
  scheduled = true;
  flushTimer = setTimeout(() => {
    flushTimer = undefined;
    void flushScopes();
  }, SCOPE_COALESCE_MS);
}

async function flushScopes(): Promise<void> {
  scheduled = false;
  if (flushing) return;
  flushing = true;
  try {
    while (scopes.size > 0) {
      const batch = [...scopes];
      scopes.clear();
      await Promise.all(batch.map((scope) => loadScope(scope).catch(() => {})));
    }
  } finally {
    flushing = false;
    // A schedule that arrived during the flush armed no second timer; its scope was drained by the
    // loop above. Cancel one that was armed just before this flush started.
    if (flushTimer !== undefined) {
      clearTimeout(flushTimer);
      flushTimer = undefined;
    }
  }
}

async function loadScope(scope: Scope): Promise<void> {
  switch (scope) {
    case 'sources':
      // Every source list is a window. A source write can change any of them (a tag, a step, a
      // delete), so all three caches are dropped; only a visible lens actually reads again.
      sourceWindow.invalidate();
      bookmarkWindow.invalidate();
      failedWindow.invalidate();
      return;
    case 'source':
      if (sourceDetailVisible() && sources.selectedHash) await sources.refresh();
      return;
    case 'claims':
      if (!claimsVisible()) {
        claims.markStale();
        return;
      }
      await claims.load(claims.topic);
      return;
    case 'topics':
      if (topicsVisible()) {
        await claims.loadTopics();
        return;
      }
      claims.markTopicsStale();
      return;
    case 'claim':
      if (claimsVisible() && claims.selectedId) await claims.open(claims.selectedId);
      return;
    case 'status':
      await system.loadStatus();
      return;
    case 'findings':
      if (findingsVisible()) await system.loadFindings();
      return;
    case 'vault':
      if (vaultVisible() && system.vaultLoaded) await system.loadVault();
      return;
    case 'vocabulary':
      if (vaultVisible()) await system.loadVocabulary();
      return;
    case 'wiki':
      await wiki.refresh();
      return;
    case 'asks':
      await asks.refresh();
      return;
  }
}

function queueLive(): void {
  if (liveTimer !== undefined) return;
  liveTimer = setTimeout(() => {
    liveTimer = undefined;
    for (const [hash, frame] of progressBuffer) {
      sources.applyProgress(hash, frame);
    }
    progressBuffer.clear();
    for (const [hash, text] of logBuffer) {
      sources.appendLog(hash, text);
    }
    logBuffer.clear();
  }, 60);
}

function mergeProgress(hash: string, data: Record<string, unknown>): void {
  const previous = progressBuffer.get(hash);
  const rawFields = (data.fields ?? {}) as Record<string, unknown>;
  const fields: Record<string, number> = { ...(previous?.fields ?? {}) };
  for (const [key, value] of Object.entries(rawFields)) {
    if (typeof value === 'number' && Number.isFinite(value)) fields[key] = value;
  }
  progressBuffer.set(hash, {
    hash,
    phase: typeof data.phase === 'string' ? data.phase : previous?.phase,
    msg: typeof data.msg === 'string' ? data.msg : previous?.msg,
    fields,
  });
  queueLive();
}

/** handleSource refreshes the list and, only when it is the open one, the source's detail. */
function handleSource(data: Record<string, unknown>): void {
  // A summary completion carries the sidecar's size. It changes neither the list nor the claims;
  // refreshing the whole list for each backfilled summary was a three-page read per summary and a
  // burst of them could starve the reads that fill the screen.
  if (typeof data.summary === 'number') {
    const hash = String(data.hash ?? '');
    if (hash && sources.selectedHash === hash) void sources.loadSummary(hash, true);
    return;
  }
  const hash = String(data.hash ?? '');
  schedule('sources');
  schedule('status');
  // A source event without a hash is about a wiki page's representation; the open source may be
  // that representation. Keep the old "refresh what is open" answer for it.
  if (!hash || sources.selectedHash === hash) schedule('source');
}

/** handleClusters tells a document run's claim write from a cluster pass's membership change. */
function handleClusters(data: Record<string, unknown>): void {
  if (typeof data.vocabulary === 'string') {
    handleVocabulary();
    return;
  }
  const hash = String(data.hash ?? '');
  if (hash) {
    // A document run wrote claims for this source: list counts, claim list and cloud changed.
    schedule('sources');
    schedule('claims');
    schedule('topics');
    schedule('status');
    if (sources.selectedHash === hash) schedule('source');
    if (claims.selectedId) schedule('claim');
    return;
  }
  // The cluster pass groups claims; it changes neither their text nor their topics. Only the open
  // claim's peers, the vault counts and the job status are different.
  schedule('status');
  schedule('vault');
  if (claims.selectedId) schedule('claim');
}

/** handleVocabulary refreshes the name catalogs and the claims that carry those names. */
function handleVocabulary(): void {
  schedule('vocabulary');
  schedule('claims');
  schedule('topics');
  schedule('sources');
  schedule('status');
  if (claims.selectedId) schedule('claim');
  if (sources.selectedHash) schedule('source');
}

function handleRun(data: Record<string, unknown>): void {
  const runID = String(data.run_id ?? '');
  const op = String(data.op ?? '');
  const step = String(data.step ?? '');
  const status = String(data.status ?? '');
  const hash = String(data.hash ?? '');

  if (status === 'running') {
    system.banner = { op: step || op, status };
    // The summary worker's counts (queued → running) are part of the status report.
    if (op === 'summary') schedule('status');
    return;
  }

  system.finishRun(op || step, status, String(data.error ?? ''));

  // A failed run may have left its source half-derived without publishing the source event that a
  // successful run sends; the open detail still has to show the step record.
  if (status !== 'succeeded' && hash && sources.selectedHash === hash) schedule('source');

  if (op === 'query') {
    // The answer may come from another window, or after this one lost the poller. Re-reading the
    // list is what turns a finished run into its saved row.
    schedule('asks');
    asks.observeRun(runID);
    return;
  }
  if (op === 'summary') {
    // A summary is a sidecar write: no source row, no claim and no detail field changed. Only the
    // summary of the source it was about is re-read — and only when that source is open — so a
    // finished backfill for another source cannot clear the "generating" state of this one.
    if (hash && sources.selectedHash === hash) void sources.loadSummary(hash, true);
    // A finished summary lowers the backfill's missing/running counts.
    schedule('status');
    return;
  }
  if (op === 'tag') {
    // A tag run writes a source's or a page's labels, not claims. The run frame names the source it
    // was about; an empty hash means the wiki page's tag run.
    if (hash) {
      schedule('sources');
      if (sources.selectedHash === hash) schedule('source');
    } else {
      schedule('wiki');
    }
    return;
  }
  if (op === 'lint') {
    schedule('findings');
    schedule('status');
    return;
  }
  if (op === 'vocabulary') {
    handleVocabulary();
    return;
  }

  // Document runs and the cluster pass. The source's own event already refreshes its open detail;
  // here only the global containers are named. A cluster pass changes memberships, not claim text.
  schedule('sources');
  schedule('claims');
  schedule('topics');
  schedule('status');
  if (claims.selectedId) schedule('claim');
  if (step === 'cluster') schedule('vault');
}

function handleFrame(frame: StreamFrame): void {
  const data = frame.data ?? {};
  switch (frame.type) {
    case 'pipeline': {
      const pipeline = data.pipeline as Pipeline | undefined;
      if (pipeline) system.applyPipeline(pipeline);
      return;
    }
    case 'progress': {
      const hash = String(data.hash ?? '');
      if (hash) mergeProgress(hash, data);
      return;
    }
    case 'log': {
      const hash = String(data.hash ?? '');
      const line = typeof data.line === 'string' ? data.line : '';
      if (!hash || !line || sources.selectedHash !== hash) return;
      logBuffer.set(hash, (logBuffer.get(hash) ?? '') + line);
      queueLive();
      return;
    }
    case 'run':
      handleRun(data);
      return;
    case 'source':
      handleSource(data);
      return;
    case 'urls':
      schedule('sources');
      schedule('status');
      return;
    case 'wiki':
      schedule('wiki');
      return;
    case 'findings':
      schedule('findings');
      return;
    case 'clusters':
      handleClusters(data);
      return;
    case 'vocabulary':
      handleVocabulary();
      return;
    case 'command':
      // One command, one GUI state: any window may have sent it (this one, another device, the
      // CLI). The store follows the address once per command id, so a frame cannot move the reader
      // twice.
      assistant.applyFrame(data);
      return;
    default:
      // Forward compatibility: unknown event types are ignored, never guessed at.
      return;
  }
}

let lastResync = 0;

/** resyncAll is the catch-up after a dropped stream or a return to the window. It re-reads the
 *  containers the reader can see once, in parallel, and marks a hidden claim list stale instead of
 *  loading it for nobody. A short guard keeps focus and visibility from firing twice. */
export async function resyncAll(force = false): Promise<void> {
  const now = Date.now();
  if (!force && now - lastResync < 1500) return;
  lastResync = now;

  if (!claimsVisible()) claims.markStale();
  if (!topicsVisible()) claims.markTopicsStale();
  sourceWindow.invalidate();
  bookmarkWindow.invalidate();
  failedWindow.invalidate();

  await Promise.all([
    system.loadStatus(),
    system.loadPipeline(),
    findingsVisible() ? system.loadFindings() : Promise.resolve(),
    wiki.load(),
    claimsVisible() ? claims.load(claims.topic) : Promise.resolve(),
    topicsVisible() ? claims.loadTopics() : Promise.resolve(),
    asks.load(),
  ]);

  await Promise.all([
    sourceDetailVisible() && sources.selectedHash ? sources.refresh() : Promise.resolve(),
    claimsVisible() && claims.selectedId ? claims.open(claims.selectedId) : Promise.resolve(),
    wiki.selectedId ? wiki.open(wiki.selectedId) : Promise.resolve(),
    asks.selectedId ? asks.open(asks.selectedId) : Promise.resolve(),
    vaultVisible() ? system.loadVault() : Promise.resolve(),
  ]);

  // A catch-up is also the place a banner from a run that ended while the stream was down can be
  // retired. The pipeline is the server's answer; no local optimism survives it.
  const pipeline = system.pipeline;
  if (
    pipeline &&
    pipeline.active === 'idle' &&
    (pipeline.asking ?? 0) === 0 &&
    (pipeline.jobs?.length ?? 0) === 0 &&
    !asks.busy
  ) {
    system.clearBanner();
  }
}

/** startSync opens the one stream and installs the catch-up points. Returns a cleanup function. */
export function startSync(): () => void {
  const stream = new EventStream({
    onFrame: (frame) => {
      noteFrame();
      handleFrame(frame);
    },
    onState: (state) => setConnectionState(state),
    onOpen: (reconnected) => {
      if (reconnected) void resyncAll(true);
    },
  });

  const onVisible = (): void => {
    if (document.visibilityState !== 'visible') return;
    if (connection.state !== 'live') stream.restart();
    void resyncAll();
  };
  const onFocus = (): void => {
    if (connection.state !== 'live') stream.restart();
    void resyncAll();
  };

  stream.start();
  document.addEventListener('visibilitychange', onVisible);
  window.addEventListener('focus', onFocus);

  return () => {
    document.removeEventListener('visibilitychange', onVisible);
    window.removeEventListener('focus', onFocus);
    stream.stop();
    if (liveTimer !== undefined) clearTimeout(liveTimer);
    if (flushTimer !== undefined) clearTimeout(flushTimer);
  };
}
