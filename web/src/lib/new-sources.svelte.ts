import { api } from './api';
import { readSignal } from './read';

/**
 * The reader's seen watermark.
 *
 * The vault keeps ONE watermark: the batch of sources — arrivals and real content changes — the
 * Sources list has not been opened on yet. The Start lens summarizes that same batch, so its
 * statistic and the list's rows can never disagree: both mark from this instant. Only the list
 * acknowledges. Opening the front door is reading a summary and settles nothing, so the marks stay
 * until the list itself is visited. Each watermark is ONE per vault, never per client (the server
 * keeps it in `.thought-keeper/reader/seen.md`): the first client that opens the list acknowledges
 * the batch for every client, so a source the phone has seen is not new on the desktop.
 *
 * This module only decides WHEN to acknowledge. It keeps the active phase and the baseline in
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

/** Ack is the acknowledgment state inside one active phase. `failed` is retried by a resync. */
type Ack = 'idle' | 'pending' | 'done' | 'failed';

/** Watermark is this window's view of the shared instant: the baseline it marks from and the value
 *  it knows of. `since` is empty when nothing is unseen (a vault's first acknowledgment). */
interface Watermark {
  since: string;
  seenAt: string;
  ack: Ack;
}

const emptyWatermark = (): Watermark => ({ since: '', seenAt: '', ack: 'idle' });

export const newSources = $state<{
  /** The vault's one watermark: the unseen batch of the Sources list, which Start summarizes. */
  sources: Watermark;
  /** A visible window with recent input is running. */
  active: boolean;
  /** The window won the single-window election. A duplicate window must never acknowledge. */
  primary: boolean;
  /** The sources this window opened: their own mark is dropped, without touching the watermark. */
  opened: string[];
  /** The baseline has been read at least once, so the front door can tell "nothing is unseen" from
   *  "not known yet". */
  loaded: boolean;
}>({ sources: emptyWatermark(), active: false, primary: false, opened: [], loaded: false });

let phase = 0;
let idleTimer: ReturnType<typeof setTimeout> | undefined;

/** isNew is the one definition of a mark: an arrival later than the baseline. */
export function isNew(importedAt: string | undefined, since: string = newSources.sources.since): boolean {
  if (!since || !importedAt) return false;
  const at = Date.parse(importedAt);
  const base = Date.parse(since);
  return Number.isFinite(at) && Number.isFinite(base) && at > base;
}

/** isSourceNew is the mark a row shows: the source arrived after the baseline AND this window has
 *  not opened it yet. Opening a source is the reader's own answer for that one row. */
export function isSourceNew(source: { hash: string; imported_at?: string }): boolean {
  if (newSources.opened.includes(source.hash)) return false;
  return isNew(source.imported_at);
}

/** isSourceUpdated is the mark a row shows for a page whose stored snapshot content was rewritten
 *  since the batch started. It is NOT "new": the source already existed. A source that also just
 *  arrived keeps the "new" mark alone — that is the stronger fact — and opening either kind drops
 *  the mark for that row in this window. */
export function isSourceUpdated(source: {
  hash: string;
  imported_at?: string;
  url_changed_at?: string;
}): boolean {
  if (newSources.opened.includes(source.hash)) return false;
  if (!source.url_changed_at) return false;
  if (isNew(source.imported_at)) return false;
  return isNew(source.url_changed_at);
}

/** markSourceOpened drops a source's own mark for the rest of the window. The shared watermark stays
 *  untouched: the batch is still the list's to acknowledge, and no other client changes. */
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
  newSources.sources.ack = 'idle';
  // Start from the newest watermark we know; the acknowledgment replaces it with the server's
  // previous value, which may be newer if another client acknowledged while this one was away.
  newSources.sources.since = newSources.sources.seenAt;
  armIdle();
}

function endPhase(): void {
  if (!newSources.active) return;
  newSources.active = false;
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
 * activateSources acknowledges the batch, once per active phase. Only the Sources list calls it:
 * the front door only summarizes, so a visit to Start settles nothing and the marks — and the
 * statistic — stay until the list itself is opened.
 */
export async function activateSources(): Promise<void> {
  const slot = newSources.sources;
  if (!newSources.primary || !newSources.active || slot.ack !== 'idle') return;
  const myPhase = phase;
  slot.ack = 'pending';
  try {
    // A local acknowledgment answers in milliseconds; a stuck connection must not hold the marks on
    // their old baseline forever.
    const result = await api.readerActive('sources', readSignal(10_000));
    if (phase !== myPhase) return;
    const seen = result.seen_at ?? '';
    const seenAt = Date.parse(seen);
    const known = Date.parse(slot.seenAt);
    // A newer watermark may have arrived while this call was in flight. A stale response must not
    // re-open marks another client already cleared.
    if (Number.isFinite(known) && Number.isFinite(seenAt) && known > seenAt) {
      slot.since = slot.seenAt;
    } else {
      // The rows keep the batch this visit settled, for the rest of the phase: the reader came for
      // it, and the front door summarizes the same baseline.
      slot.since = result.first || !result.since ? '' : result.since;
      if (seen) slot.seenAt = seen;
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
 * applySharedSeen applies a watermark another client acknowledged (an SSE frame or a resync). It only
 * moves forward, and it clears this window's marks: the batch is seen somewhere. The client that
 * acknowledged is protected by its own `seenAt` already holding the value, so its own event is a
 * no-op.
 */
export function applySharedSeen(seenAt: string): void {
  if (!seenAt) return;
  const slot = newSources.sources;
  const next = Date.parse(seenAt);
  const current = Date.parse(slot.seenAt);
  if (!Number.isFinite(next)) return;
  if (Number.isFinite(current) && next <= current) return;
  slot.seenAt = seenAt;
  slot.since = seenAt;
}

/** refreshNewSources reads the watermark after a reconnect or a return to the window. It never
 *  acknowledges. */
export async function refreshNewSources(): Promise<void> {
  try {
    const state = await api.reader(readSignal(10_000));
    applySharedSeen(state.sources_seen_at ?? '');
  } catch {
    /* the stream and the next reads try again */
  } finally {
    newSources.loaded = true;
  }
}

/** retryNewSources gives a failed acknowledgment another chance, on the next resync. */
export function retryNewSources(): void {
  if (!newSources.active) return;
  if (newSources.sources.ack === 'failed') newSources.sources.ack = 'idle';
}
