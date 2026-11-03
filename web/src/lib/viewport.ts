/**
 * The shell's height is the area the reader SEES.
 *
 * `100dvh` is the right idea and not enough on a phone. An installed PWA has no browser chrome, so
 * the dynamic viewport should be the window — but the platform draws the system bars (status,
 * gesture/navigation) over that window, and their insets are not always applied after a reload: a
 * reload in Brave on Android lays the page out against the full display, the footer navigation
 * slides under the navigation bar and out of the window, and nothing brings it back until a real
 * resize — a rotation — re-measures. The visual viewport is what the compositor actually shows, so
 * it is the reading the shell is sized to; the safe-area insets are then reserved in CSS
 * (`padding` on `.app-shell`), which keeps the footer above the system bar even while that bar is
 * overlaid.
 *
 * A pinch-zoom changes the visual viewport without resizing the app: the shell keeps its height and
 * the reader pans, exactly like any other page.
 */

/** viewportHeight picks the shell's height from a visual-viewport-like reading.
 *
 *  It returns 0 when the reading must be ignored (a pinch-zoom) or is unusable; the caller then
 *  keeps the height it has. The result is floored, so a fractional viewport never leaves a one-pixel
 *  slice of it outside the window. */
export function viewportHeight(
  view: { height: number; scale?: number } | null | undefined,
  fallback: number,
): number {
  if (view) {
    if ((view.scale ?? 1) > 1.01) return 0;
    if (Number.isFinite(view.height) && view.height > 0) return Math.floor(view.height);
  }
  if (Number.isFinite(fallback) && fallback > 0) return Math.floor(fallback);
  return 0;
}

/** trackViewportHeight publishes the visible height as `--app-h` (see styles/app.css) and keeps it
 *  current through every event that can change it without a page load. */
export function trackViewportHeight(): void {
  const root = document.documentElement;
  const apply = (): void => {
    const height = viewportHeight(window.visualViewport, window.innerHeight);
    if (height > 0) root.style.setProperty('--app-h', `${height}px`);
  };

  apply();
  window.addEventListener('resize', apply);
  window.addEventListener('orientationchange', apply);
  // A page restored from the back/forward cache was laid out for another window state.
  window.addEventListener('pageshow', apply);
  // The browser chrome collapsing, and the on-screen keyboard: both resize the visual viewport.
  window.visualViewport?.addEventListener('resize', apply);
}

/** freezeDocumentScroll keeps the document itself at the top. Every position belongs to an inner
 *  scroller (lib/scroll-memory.ts), so an offset the browser restores for the document is always
 *  wrong — and on a phone it can push the footer navigation below the screen. */
export function freezeDocumentScroll(): void {
  if ('scrollRestoration' in history) history.scrollRestoration = 'manual';
  const reset = (): void => window.scrollTo(0, 0);
  reset();
  window.addEventListener('pageshow', reset);
}
