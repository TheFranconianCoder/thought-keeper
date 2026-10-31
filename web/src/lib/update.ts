/**
 * Notice that the app has moved on.
 *
 * The installed PWA is a single-page app: a window keeps the bundle it loaded, and the service
 * worker deliberately changes no file between builds, so nothing would ever tell that window that
 * the binary now serves a different UI. The build writes `/build.json` with the entry chunk it
 * produced; a window knows the module it is running (`import.meta.url`) and compares the two.
 */

export interface BuildManifest {
  entry?: string;
}

/** UpdateAction is the one thing a check may ask for. */
export type UpdateAction = 'none' | 'reload' | 'notify';

/** entryOf turns a module URL into the path the manifest names. */
export function entryOf(moduleUrl: string): string {
  try {
    return new URL(moduleUrl).pathname;
  } catch {
    return moduleUrl;
  }
}

/** isStale reports whether the served build differs from the one this window is running. */
export function isStale(manifest: BuildManifest | null | undefined, entry: string): boolean {
  return Boolean(manifest?.entry && entry && manifest.entry !== entry);
}

/**
 * decide turns a check into its action. A visible window is never reloaded under the reader's
 * hands — an unsaved wiki page or a half-typed question would be lost — so it is told instead; a
 * hidden window is reloaded at once, which is exactly the moment a phone backgrounds the app.
 * `reloadedFor` is the entry this window already reloaded for: if the build still looks stale after
 * that (a stale cache), the window says so instead of reloading in a loop.
 */
export function decide(
  manifest: BuildManifest | null | undefined,
  entry: string,
  opts: { hidden: boolean; reloadedFor?: string },
): UpdateAction {
  if (!isStale(manifest, entry)) return 'none';
  if (manifest?.entry && manifest.entry === opts.reloadedFor) return 'notify';
  return opts.hidden ? 'reload' : 'notify';
}
