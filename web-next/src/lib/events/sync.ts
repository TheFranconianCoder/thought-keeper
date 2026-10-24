import type { Pipeline, ProgressFrame, StreamFrame } from '../api/types';
import { EventStream } from './event-stream';
import { noteFrame, setConnectionState, connection } from '../stores/connection.svelte';
import { system } from '../stores/system.svelte';
import { sources } from '../stores/sources.svelte';
import { claims } from '../stores/claims.svelte';
import { wiki } from '../stores/wiki.svelte';
import { asks } from '../stores/asks.svelte';

/**
 * The event pipeline.
 *
 *  SSE frames are not handled one request at a time. They are folded into two lanes:
 *
 *  1. LIVE — progress counters and log lines. These carry their payload; they are merged per source
 *     and flushed once per animation-sized tick. A run that emits hundreds of log lines therefore
 *     causes a handful of reactive updates, not hundreds.
 *
 *  2. STATE — "this container changed". These are coalesced into a set of scopes. Each scope is
 *     re-read at most once per batch, and a batch that arrives while another is loading is picked up
 *     by the next turn. That is what keeps a burst of events from firing overlapping requests.
 *
 *  Every store loader has its own generation counter, so even a late response cannot overwrite a
 *  newer one.
 */

type Scope = 'sources' | 'claims' | 'clusters' | 'findings' | 'wiki' | 'asks' | 'vocabulary' | 'status';

const scopes = new Set<Scope>();
let scheduled = false;
let flushing = false;
let liveTimer: ReturnType<typeof setTimeout> | undefined;

const progressBuffer = new Map<string, ProgressFrame>();
const logBuffer = new Map<string, string>();

function schedule(scope: Scope): void {
  scopes.add(scope);
  if (scheduled || flushing) return;
  scheduled = true;
  queueMicrotask(() => void flushScopes());
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
  }
}

async function loadScope(scope: Scope): Promise<void> {
  switch (scope) {
    case 'sources':
      await Promise.all([sources.loadList(), system.loadStatus()]);
      if (sources.selectedHash) await sources.refresh();
      return;
    case 'claims':
      await Promise.all([claims.load(claims.topic), claims.loadTopics()]);
      if (claims.selectedId) await claims.open(claims.selectedId);
      return;
    case 'clusters':
      await Promise.all([
        sources.loadList(),
        claims.load(claims.topic),
        claims.loadTopics(),
        system.loadStatus(),
      ]);
      if (sources.selectedHash) await sources.refresh();
      if (claims.selectedId) await claims.open(claims.selectedId);
      if (system.vaultLoaded) await system.loadVault();
      return;
    case 'findings':
      await system.loadFindings();
      return;
    case 'wiki':
      await wiki.refresh();
      return;
    case 'asks':
      await asks.refresh();
      return;
    case 'vocabulary':
      await system.loadVocabulary();
      if (system.vaultLoaded) await system.loadVault();
      return;
    case 'status':
      await system.loadStatus();
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

function handleRun(data: Record<string, unknown>): void {
  const runID = String(data.run_id ?? '');
  const op = String(data.op ?? '');
  const step = String(data.step ?? '');
  const status = String(data.status ?? '');

  if (status === 'running') {
    system.banner = { op: step || op, status };
    // The summary worker's counts (queued → running) are part of the status report.
    if (op === 'summary') schedule('status');
    return;
  }

  system.finishRun(op || step, status, String(data.error ?? ''));

  if (op === 'query') {
    asks.observeRun(runID);
    return;
  }
  if (op === 'summary') {
    // A summary is a sidecar write: no source row, no claim and no detail field changed. Only the
    // summary of the source it was about is re-read — and only when that source is open — so a
    // finished backfill for another source cannot clear the "generating" state of this one.
    const hash = String(data.hash ?? '');
    if (hash && sources.selectedHash === hash) void sources.loadSummary(hash, true);
    // A finished summary lowers the backfill's missing/running counts.
    schedule('status');
    return;
  }
  if (op === 'tag') {
    // A tag run writes a source's or a page's labels, not claims. The run frame names the source it
    // was about; an empty hash means the wiki page's tag run.
    if (String(data.hash ?? '')) schedule('sources');
    else schedule('wiki');
    return;
  }
  if (op === 'lint') {
    schedule('findings');
    schedule('status');
    return;
  }
  if (op === 'vocabulary') {
    schedule('vocabulary');
    schedule('clusters');
    return;
  }
  // Document steps and the cluster pass change claims, source counters and the vault report.
  schedule('sources');
  schedule('claims');
  schedule('status');
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
      // A summary completion carries the sidecar's size. It changes neither the list nor the
      // claims; refreshing the whole list for each backfilled summary was a three-page read per
      // summary and a burst of them could starve the reads that fill the screen.
      if (typeof data.summary === 'number') {
        const hash = String(data.hash ?? '');
        if (hash && sources.selectedHash === hash) void sources.loadSummary(hash, true);
        return;
      }
      schedule('sources');
      return;
    case 'urls':
      schedule('sources');
      return;
    case 'wiki':
      schedule('wiki');
      return;
    case 'findings':
      schedule('findings');
      return;
    case 'clusters':
      schedule('clusters');
      if (typeof data.vocabulary === 'string') schedule('vocabulary');
      return;
    case 'vocabulary':
      schedule('vocabulary');
      return;
    default:
      // Forward compatibility: unknown event types are ignored, never guessed at.
      return;
  }
}

let lastResync = 0;

/** resyncAll is the catch-up after a dropped stream or a return to the window. It re-reads every
 *  container once, in parallel. A short guard keeps focus and visibility from firing twice. */
export async function resyncAll(force = false): Promise<void> {
  const now = Date.now();
  if (!force && now - lastResync < 1500) return;
  lastResync = now;

  await Promise.all([
    system.loadStatus(),
    system.loadPipeline(),
    sources.loadList(),
    system.loadFindings(),
    wiki.load(),
    claims.load(claims.topic),
    claims.loadTopics(),
    asks.load(),
  ]);

  await Promise.all([
    sources.selectedHash ? sources.refresh() : Promise.resolve(),
    claims.selectedId ? claims.open(claims.selectedId) : Promise.resolve(),
    wiki.selectedId ? wiki.open(wiki.selectedId) : Promise.resolve(),
    asks.selectedId ? asks.open(asks.selectedId) : Promise.resolve(),
    system.vaultLoaded ? system.loadVault() : Promise.resolve(),
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
  };
}
