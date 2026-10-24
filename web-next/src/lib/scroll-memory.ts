// Scroll position belongs to the history entry. Hash routing keeps one document, so the browser does
// not restore positions for `#/claims/a` → `#/claims/b`; the entry carries them instead.
//
// A page has more than one scroller (the shell and its reading content, the sticky list panes, the
// scrollable clouds), so the state is a map keyed by the scroller's role. Every position is written
// to the current entry and read back when that entry becomes current again. A fresh entry — a direct
// link, a new hash — has no positions and therefore starts at the top, exactly like a normal page.

type ScrollPositions = Record<string, number>;

export interface ScrollMemoryAction {
  update(key: string): void;
  destroy(): void;
}

const scrollers = new Map<string, HTMLElement>();
const restoreSeq = new Map<string, number>();
let suppressWrites = false;
let suppressTimer: ReturnType<typeof setTimeout> | undefined;

/** beginRouteChange runs before a new history entry becomes current. A scroll event from the
 *  OUTGOING page can fire while the new entry is already current; without this guard it wrote the
 *  old offset into the new entry and the next page opened pre-scrolled. */
export function beginRouteChange(): void {
  suppressWrites = true;
  if (suppressTimer) clearTimeout(suppressTimer);
  suppressTimer = setTimeout(() => {
    suppressWrites = false;
    suppressTimer = undefined;
  }, 180);
}

function readState(): Record<string, unknown> {
  const state = history.state;
  return state && typeof state === 'object' ? (state as Record<string, unknown>) : {};
}

function readPositions(): ScrollPositions {
  const raw = readState().tkScroll;
  if (typeof raw === 'number') return { page: raw };
  if (!raw || typeof raw !== 'object') return {};
  const out: ScrollPositions = {};
  for (const [key, value] of Object.entries(raw)) {
    if (typeof value === 'number' && Number.isFinite(value)) out[key] = value;
  }
  return out;
}

function writePosition(key: string, y: number): void {
  if (!key || !Number.isFinite(y)) return;
  const state = readState();
  const positions = readPositions();
  if (positions[key] === y) return;
  positions[key] = Math.max(0, Math.round(y));
  history.replaceState({ ...state, tkScroll: positions }, '');
}

/** rememberScroll is the Svelte action for one scroll container. The key names its role, not its
 *  route: a split view's list keeps one key while the reading pane's key carries the item id. */
export function rememberScroll(node: HTMLElement, initialKey = 'page'): ScrollMemoryAction {
  let key = initialKey;
  let attached = false;

  const save = (): void => {
    if (!key || suppressWrites) return;
    writePosition(key, node.scrollTop);
  };

  const detach = (): void => {
    if (!attached) return;
    node.removeEventListener('scroll', save);
    if (scrollers.get(key) === node) scrollers.delete(key);
    attached = false;
  };

  const attach = (): void => {
    if (!key || attached) return;
    node.addEventListener('scroll', save, { passive: true });
    scrollers.set(key, node);
    attached = true;
    scheduleRestore(key, node);
  };

  attach();

  return {
    update(next: string): void {
      if (next === key) return;
      detach();
      key = next;
      attach();
    },
    destroy(): void {
      detach();
    },
  };
}

/** setPagePosition seeds the CURRENT history entry's page offset. A back button that pushes a fresh
 *  list entry would otherwise start at the top; the position belongs to the place being returned to. */
export function setPagePosition(y: number): void {
  writePosition('page', y);
}

/** restoreScroll re-applies the scrollers the current history entry knows about. The page key is
 *  always restored (a new page starts at the top); a list or cloud with no entry in this history
 *  entry keeps its position, so a persistent list pane does not jump when the reading pane changes. */
export function restoreScroll(): void {
  const positions = readPositions();
  for (const [key, node] of scrollers) {
    if (key === 'page' || Object.prototype.hasOwnProperty.call(positions, key)) {
      scheduleRestore(key, node);
    }
  }
}

/** scheduleRestore writes the entry's saved position as soon as the scroller is tall enough. Lists
 *  are loaded asynchronously, so a single write would be clamped to 0 before the rows arrive. */
function scheduleRestore(key: string, node: HTMLElement): void {
  if (!key) return;
  const seq = (restoreSeq.get(key) ?? 0) + 1;
  restoreSeq.set(key, seq);
  const target = readPositions()[key] ?? 0;
  let attempt = 0;

  const apply = (): void => {
    if (restoreSeq.get(key) !== seq || scrollers.get(key) !== node) return;
    node.scrollTop = target;
    if (target <= 0) return;
    const reached = node.scrollTop >= target - 1;
    const tall = node.scrollHeight - node.clientHeight >= target;
    if ((reached && tall) || attempt >= 7) return;
    attempt++;
    window.setTimeout(apply, attempt < 3 ? 40 : 180);
  };

  requestAnimationFrame(apply);
}
