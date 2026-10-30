/** The Sources lens is one page scroller, and the detail's back button opens a NEW #/sources history
 *  entry — which has no scroll state of its own. Remembering the last list offset lets that entry be
 *  seeded with it, so returning from a source lands where the reader left the list. */
let top = 0;

export function rememberSourcesTop(y: number): void {
  if (!Number.isFinite(y)) return;
  top = Math.max(0, Math.round(y));
}

export function sourcesTop(): number {
  return top;
}
