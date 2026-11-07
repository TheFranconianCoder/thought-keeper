import { api } from './api';
import { readSignal } from './read';

/**
 * The shared "new" watermark.
 *
 * A source is NEW when it arrived after the seen watermark. The watermark is ONE per vault, never
 * per client (the server keeps it in `.thought-keeper/reader/seen.md`): the first client that
 * becomes active while Start or Sources is visible acknowledges the batch for every client, so a
 * source the phone has seen is not new on the desktop.
 *
 * This module only decides WHEN to acknowledge. It keeps the active phase and its baseline in
 * memory; no client identity and no per-client storage exist.
 *
 * An active phase is a visible window with input in the last 15 minutes. Hiding the window or going
 * idle ends it; the next input starts a new one, which acknowledges again and therefore sees what
 * arrived in between as new.
 */

const IDLE_MS = 15 * 60 * 1000;
const ACTIVITY_EVENTS = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const;

/** Ack is the acknowledgment's state inside one active phase. `failed` is retried by a resync. */
type Ack = 'idle' | 'pending' | 'done' | 'failed';

export const newSources = $state<{
  /** The phase's baseline: a source with a later arrival is new. Empty means "mark nothing". */
  since: string;
  /** The newest shared watermark this client knows of. */
  seenAt: string;
  /** A visible window with recent input is running. */
  active: boolean;
  /** This phase's acknowledgment. */
  ack: Ack;
  /** The window won the single-window election. A duplicate window must never acknowledge. */
  primary: boolean;
}>({ since: '', seenAt: '', active: false, ack: 'idle', primary: false });

let phase = 0;
let idleTimer: ReturnType<typeof setTimeout> | undefined;

/** isNew is the one definition of the mark: an arrival later than the phase's baseline. */
export function isNew(importedAt: string | undefined, since: string = newSources.since): boolean {
  if (!since || !importedAt) return false;
  const at = Date.parse(importedAt);
  const base = Date.parse(since);
  return Number.isFinite(at) && Number.isFinite(base) && at > base;
}

/** arrivalWindowStart extends the Start card's three days to the last acknowledgment when that was
 *  earlier, so an arrival the reader has not seen is actually on the card. */
export function arrivalWindowStart(now: number, since: string, minimumDays = 3): string {
  const floor = now - minimumDays * 86_400_000;
  const base = Date.parse(since);
  return new Date(Number.isFinite(base) ? Math.min(floor, base) : floor).toISOString();
}

/** arrivalWindowExtended says whether the card should name the last visit instead of "last 3 days". */
export function arrivalWindowExtended(now: number, since: string, minimumDays = 3): boolean {
  const base = Date.parse(since);
  return Number.isFinite(base) && base < now - minimumDays * 86_400_000;
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
  newSources.ack = 'idle';
  // Start from the newest watermark we know; the acknowledgment replaces it with the server's
  // previous value, which may be newer if another client acknowledged while this one was away.
  newSources.since = newSources.seenAt;
  armIdle();
}

function endPhase(): void {
  if (!newSources.active) return;
  newSources.active = false;
  newSources.ack = 'idle';
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
 * activateNewSources acknowledges the batch, once per active phase. The views call it when Start or
 * Sources is visible: a wake on a claim or a source page must not consume the arrivals.
 */
export async function activateNewSources(): Promise<void> {
  if (!newSources.primary || !newSources.active || newSources.ack !== 'idle') return;
  const myPhase = phase;
  newSources.ack = 'pending';
  try {
    // A local acknowledgment answers in milliseconds; a stuck connection must not hold the Start
    // card on its skeletons forever.
    const result = await api.readerActive(readSignal(10_000));
    if (phase !== myPhase) return;
    const seen = result.seen_at ?? '';
    const seenAt = Date.parse(seen);
    const known = Date.parse(newSources.seenAt);
    // A newer watermark may have arrived while this call was in flight. A stale response must not
    // re-open marks another client already cleared.
    if (Number.isFinite(known) && Number.isFinite(seenAt) && known > seenAt) {
      newSources.since = newSources.seenAt;
    } else {
      newSources.since = result.first || !result.since ? '' : result.since;
      if (seen) newSources.seenAt = seen;
    }
    newSources.ack = 'done';
  } catch {
    // The server is unreachable or repairing the index. Keep what we know; a resync retries, and the
    // next phase starts a new attempt.
    if (phase === myPhase) {
      newSources.since = newSources.seenAt;
      newSources.ack = 'failed';
    }
  }
}

/**
 * applySharedSeen applies a watermark another client acknowledged (an SSE frame or a resync). It
 * only moves forward, and it clears this client's marks: the sources are seen somewhere. The client
 * that acknowledged is protected by its own `seenAt` already holding the value, so its own event is
 * a no-op.
 */
export function applySharedSeen(seenAt: string): void {
  if (!seenAt) return;
  const next = Date.parse(seenAt);
  const current = Date.parse(newSources.seenAt);
  if (!Number.isFinite(next)) return;
  if (Number.isFinite(current) && next <= current) return;
  newSources.seenAt = seenAt;
  newSources.since = seenAt;
}

/** refreshNewSources reads the shared watermark after a reconnect or a return to the window. It
 *  never acknowledges. */
export async function refreshNewSources(): Promise<void> {
  try {
    const state = await api.reader(readSignal(10_000));
    applySharedSeen(state.seen_at ?? '');
  } catch {
    /* the stream and the next reads try again */
  }
}

/** retryNewSources gives a failed acknowledgment another chance, on the next resync. */
export function retryNewSources(): void {
  if (newSources.active && newSources.ack === 'failed') newSources.ack = 'idle';
}
