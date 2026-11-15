import type { ReaderScope } from './api/types';
import { api } from './api';
import { readSignal } from './read';

/**
 * The reader's two seen watermarks.
 *
 * Start's watermark drives the statistic the front door shows; Sources' watermark drives the new
 * and updated marks of its rows. Opening Start settles the front door alone, so the Sources marks
 * stay until the list is actually visited; opening Sources settles BOTH, because the list answers
 * the same batch the front door summarizes. Each watermark is ONE per vault, never per client (the server keeps both in
 * `.thought-keeper/reader/seen.md`): the first client active on a lens acknowledges that scope for
 * every client, so a source the phone has seen is not new on the desktop.
 *
 * This module only decides WHEN to acknowledge. It keeps the active phase and the baselines in
 * memory; no client identity and no per-client storage exist.
 *
 * Opening a source is the reader's own answer for that ONE row: its mark drops for the rest of the
 * window, in memory. The shared watermark is untouched, so nothing is written and no other client
 * is affected.
 *
 * An active phase is a visible window with input in the last 15 minutes. Hiding the window or going
 * idle ends it; the next input starts a new one, which acknowledges again and therefore sees what
 * arrived in between as new.
 */

const IDLE_MS = 15 * 60 * 1000;
const ACTIVITY_EVENTS = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const;

/** Ack is one scope's acknowledgment state inside one active phase. `failed` is retried by a
 *  resync. */
type Ack = 'idle' | 'pending' | 'done' | 'failed';

/** Watermark is one lens's in-memory view: the baseline it marks from and the shared value it knows
 *  of. `since` is empty when nothing is new there (a scope's first visit). */
interface Watermark {
  since: string;
  seenAt: string;
  ack: Ack;
}

const emptyWatermark = (): Watermark => ({ since: '', seenAt: '', ack: 'idle' });

export const newSources = $state<{
  /** The Start lens's baseline: the front door's statistic reads from it. */
  start: Watermark;
  /** The Sources lens's baseline: its rows' new/updated marks read from it. */
  sources: Watermark;
  /** A visible window with recent input is running. */
  active: boolean;
  /** The window won the single-window election. A duplicate window must never acknowledge. */
  primary: boolean;
  /** The sources this window opened: their own mark is dropped, without touching the watermark. */
  opened: string[];
}>({ start: emptyWatermark(), sources: emptyWatermark(), active: false, primary: false, opened: [] });

let phase = 0;
let idleTimer: ReturnType<typeof setTimeout> | undefined;

/** isNew is the one definition of a mark: an arrival later than the baseline. The Sources baseline
 *  is the default; the Start statistic asks with its own. */
export function isNew(importedAt: string | undefined, since: string = newSources.sources.since): boolean {
  if (!since || !importedAt) return false;
  const at = Date.parse(importedAt);
  const base = Date.parse(since);
  return Number.isFinite(at) && Number.isFinite(base) && at > base;
}

/** isSourceNew is the mark a row shows: the source arrived after the Sources baseline AND this
 *  window has not opened it yet. Opening a source is the reader's own answer for that one row. */
export function isSourceNew(source: { hash: string; imported_at?: string }): boolean {
  if (newSources.opened.includes(source.hash)) return false;
  return isNew(source.imported_at, newSources.sources.since);
}

/** isSourceUpdated is the mark a row shows for a page whose stored snapshot content was rewritten
 *  since the reader's last Sources visit. It is NOT "new": the source already existed. A source
 *  that also just arrived keeps the "new" mark alone — that is the stronger fact — and opening
 *  either kind drops the mark for that row in this window. */
export function isSourceUpdated(source: {
  hash: string;
  imported_at?: string;
  url_changed_at?: string;
}): boolean {
  if (newSources.opened.includes(source.hash)) return false;
  if (!source.url_changed_at) return false;
  if (isNew(source.imported_at, newSources.sources.since)) return false;
  return isNew(source.url_changed_at, newSources.sources.since);
}

/** markSourceOpened drops a source's own mark for the rest of the window. The shared watermark stays
 *  untouched: the batch is still Sources' to acknowledge, and no other client changes. */
export function markSourceOpened(hash: string): void {
  if (!hash || newSources.opened.includes(hash)) return;
  newSources.opened.push(hash);
}

function clearIdle(): void {
  if (idleTimer !== undefined) {
    clearTimeout(idleTimer);
    idleTimer = undefined;
  }
}

function armIdle(): void {
  clearIdle();
  idleTimer = setTimeout(() => endPhase(), IDLE_MS);
}

function beginPhase(): void {
  if (newSources.active) return;
  phase++;
  newSources.active = true;
  newSources.start.ack = 'idle';
  newSources.sources.ack = 'idle';
  // Start from the newest watermark we know; the acknowledgment replaces it with the server's
  // previous value, which may be newer if another client acknowledged while this one was away.
  newSources.start.since = newSources.start.seenAt;
  newSources.sources.since = newSources.sources.seenAt;
  armIdle();
}

function endPhase(): void {
  if (!newSources.active) return;
  newSources.active = false;
  newSources.start.ack = 'idle';
  newSources.sources.ack = 'idle';
  clearIdle();
}

function noteActivity(): void {
  if (!newSources.active) {
    beginPhase();
    return;
  }
  armIdle();
}

/** initNewSources wires the presence engine once, before the app renders. */
export function initNewSources(): void {
  if (typeof window === 'undefined' || typeof document === 'undefined') return;
  if (document.visibilityState !== 'hidden') beginPhase();
  for (const event of ACTIVITY_EVENTS) {
    window.addEventListener(event, noteActivity, { passive: true });
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') endPhase();
    else noteActivity();
  });
  // A pagehide means the window is leaving (closed or reloaded). The idle timer would not fire on a
  // suspended tab, so the phase ends here. pagehide while already hidden documented the end earlier.
  window.addEventListener('pagehide', () => {
    if (document.visibilityState === 'visible') endPhase();
  });
  window.addEventListener('pageshow', () => {
    if (document.visibilityState === 'visible') noteActivity();
  });
}

/**
 * activateStart acknowledges the Start lens, once per active phase. The front door calls it when
 * it is visible; no other lens, and no wake on a claim or a source page, consumes the statistic.
 * It never settles the Sources marks: the reader has only seen the summary.
 */
export async function activateStart(): Promise<void> {
  await activate('start');
}

/**
 * activateSources acknowledges the Sources lens, once per active phase. Only Sources calls it: until
 * the reader actually visits the list, its new/updated marks stay. The visit settles Start's
 * statistic as well — the list answers the batch the front door only summarized.
 */
export async function activateSources(): Promise<void> {
  await activate('sources');
}

async function activate(scope: ReaderScope): Promise<void> {
  const slot = newSources[scope];
  if (!newSources.primary || !newSources.active || slot.ack !== 'idle') return;
  const myPhase = phase;
  slot.ack = 'pending';
  try {
    // A local acknowledgment answers in milliseconds; a stuck connection must not hold the Start
    // card on its skeletons forever.
    const result = await api.readerActive(scope, readSignal(10_000));
    if (phase !== myPhase) return;
    const seen = result.seen_at ?? '';
    const seenAt = Date.parse(seen);
    const known = Date.parse(slot.seenAt);
    // A newer watermark may have arrived while this call was in flight. A stale response must not
    // re-open marks another client already cleared.
    if (Number.isFinite(known) && Number.isFinite(seenAt) && known > seenAt) {
      slot.since = slot.seenAt;
    } else {
      slot.since = result.first || !result.since ? '' : result.since;
      if (seen) slot.seenAt = seen;
    }
    // A visit can settle the OTHER lens too: opening Sources acknowledges the batch the front door
    // summarizes, so the Start statistic clears here exactly as in every other window. The scope
    // just acknowledged keeps its own baseline above, so its marks still show for this phase.
    for (const settled of result.settled ?? []) {
      if (settled.scope === scope) continue;
      applySharedSeen(settled.scope, settled.seen_at ?? '');
    }
    slot.ack = 'done';
  } catch {
    // The server is unreachable or repairing the index. Keep what we know; a resync retries, and the
    // next phase starts a new attempt.
    if (phase === myPhase) {
      slot.since = slot.seenAt;
      slot.ack = 'failed';
    }
  }
}

/**
 * applySharedSeen applies a watermark another client acknowledged (an SSE frame or a resync). It
 * only moves forward, and it clears this client's marks for that scope: the sources are seen
 * somewhere. The client that acknowledged is protected by its own `seenAt` already holding the
 * value, so its own event is a no-op.
 */
export function applySharedSeen(scope: ReaderScope, seenAt: string): void {
  if (!seenAt) return;
  const slot = newSources[scope];
  const next = Date.parse(seenAt);
  const current = Date.parse(slot.seenAt);
  if (!Number.isFinite(next)) return;
  if (Number.isFinite(current) && next <= current) return;
  slot.seenAt = seenAt;
  slot.since = seenAt;
}

/** refreshNewSources reads both watermarks after a reconnect or a return to the window. It never
 *  acknowledges. */
export async function refreshNewSources(): Promise<void> {
  try {
    const state = await api.reader(readSignal(10_000));
    applySharedSeen('start', state.start_seen_at ?? '');
    applySharedSeen('sources', state.sources_seen_at ?? '');
  } catch {
    /* the stream and the next reads try again */
  }
}

/** retryNewSources gives a failed acknowledgment another chance, on the next resync. */
export function retryNewSources(): void {
  if (!newSources.active) return;
  if (newSources.start.ack === 'failed') newSources.start.ack = 'idle';
  if (newSources.sources.ack === 'failed') newSources.sources.ack = 'idle';
}
