/**
 * Scroll-to-top: one button for every list.
 *
 * A list scrolls in one of two places — the page itself, or the pane beside the reading side in the
 * split layouts (Claims, Wiki, Ask). Every scrollable container registers itself with
 * `scrollTopTarget`; the button follows the one that is scrolled furthest, so a view never has to
 * know which layout is active, and the narrow layout (where the panes fall back to the page) needs
 * no extra case.
 */

/** SCROLL_TOP_THRESHOLD is how far a list has to move before the button is worth showing: roughly
 *  one screen of a phone, so a small list never gets a control it does not need. */
export const SCROLL_TOP_THRESHOLD = 480;

export const scrollTop = $state<{ visible: boolean }>({ visible: false });

/** furthestScrolled names the container that scrolled furthest, or -1 when none is past the
 *  threshold. It is pure, so the choice is tested without a DOM. */
export function furthestScrolled(tops: number[], threshold = SCROLL_TOP_THRESHOLD): number {
  let best = -1;
  let bestTop = threshold;
  tops.forEach((top, index) => {
    if (top > bestTop) {
      best = index;
      bestTop = top;
    }
  });
  return best;
}

const targets = new Set<HTMLElement>();
let active: HTMLElement | null = null;
let listeningResize = false;

/** ensureResizeListener binds the layout change once: a split pane that a resize hides must stop
 *  being the button's target even though it keeps its old scrollTop. */
function ensureResizeListener(): void {
  if (listeningResize || typeof window === 'undefined') return;
  listeningResize = true;
  window.addEventListener('resize', refresh, { passive: true });
}

function refresh(): void {
  // A container hidden by a layout change keeps its old scrollTop and must not win the race; it is
  // filtered here rather than dropped, because resizing back re-reveals the same element.
  const live = [...targets].filter((el) => el.isConnected && el.clientHeight > 0);
  const index = furthestScrolled(live.map((el) => el.scrollTop));
  active = index === -1 ? null : live[index];
  scrollTop.visible = active !== null;
}

/**
 * scrollTopTarget registers a scroll container. The element decides when it is the page and when
 * it is a pane; the button simply follows it.
 */
export function scrollTopTarget(node: HTMLElement): { destroy: () => void } {
  targets.add(node);
  ensureResizeListener();
  const onScroll = (): void => refresh();
  node.addEventListener('scroll', onScroll, { passive: true });
  refresh();
  return {
    destroy() {
      node.removeEventListener('scroll', onScroll);
      targets.delete(node);
      refresh();
    },
  };
}

/** scrollToTop scrolls the container the reader last moved back to its first row. */
export function scrollToTop(): void {
  const el = active;
  if (!el) return;
  const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
  el.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' });
}
