import { decide, entryOf, type BuildManifest } from './update';

/**
 * The update watch.
 *
 * A local app is restarted far more often than a website is deployed, and its PWA window outlives
 * both. The check is cheap and local (`/build.json`, never cached), so it runs on every catch-up
 * point — a stream reconnect, a return to the window, a restored page — and once every five minutes
 * while the window is visible. A hidden window reloads at once; a visible one gets a quiet notice,
 * because a reload under the reader's hands would throw away a draft.
 */

/** This window's build: the entry chunk the document loaded, which is exactly what `/build.json`
 *  names. It is read from the document rather than from `import.meta.url`, because a code-split
 *  chunk that happens to hold this module must never be mistaken for the entry. */
function documentEntry(): string {
  const src = document.querySelector('script[type="module"][src]')?.getAttribute('src');
  if (!src) return entryOf(import.meta.url);
  try {
    return entryOf(new URL(src, document.baseURI).href);
  } catch {
    return src;
  }
}

const ENTRY = documentEntry();
const RELOADED_KEY = 'tk-update-reloaded';
const CHECK_MS = 5 * 60 * 1000;

export const update = $state<{ pending: string }>({ pending: '' });

function reloadedFor(): string {
  try {
    return sessionStorage.getItem(RELOADED_KEY) ?? '';
  } catch {
    return '';
  }
}

function rememberReload(entry: string): void {
  try {
    sessionStorage.setItem(RELOADED_KEY, entry);
  } catch {
    /* storage can be unavailable; the check simply runs again next time */
  }
}

/** reloadNow loads the build the service already serves. */
export function reloadNow(): void {
  if (update.pending) rememberReload(update.pending);
  window.location.reload();
}

async function readManifest(): Promise<BuildManifest | null> {
  try {
    const res = await fetch('/build.json', { cache: 'no-store' });
    if (!res.ok) return null;
    return (await res.json()) as BuildManifest;
  } catch {
    // The app is not running (or the file is missing): nothing to compare, nothing to say.
    return null;
  }
}

let inFlight: Promise<void> | null = null;

/** checkForUpdate compares the served build with this window's, at most once at a time. */
export function checkForUpdate(): Promise<void> {
  // Dev has no build manifest and HMR already reloads; the check is a production concern.
  if (!import.meta.env.PROD) return Promise.resolve();
  if (inFlight) return inFlight;
  inFlight = (async () => {
    const manifest = await readManifest();
    const action = decide(manifest, ENTRY, {
      hidden: document.visibilityState !== 'visible',
      reloadedFor: reloadedFor(),
    });
    if (action === 'none') return;
    if (action === 'reload') {
      rememberReload(manifest?.entry ?? '');
      window.location.reload();
      return;
    }
    update.pending = manifest?.entry ?? 'stale';
  })().finally(() => {
    inFlight = null;
  });
  return inFlight;
}

/** startUpdateWatch installs the triggers. Returns a cleanup function. */
export function startUpdateWatch(): () => void {
  if (!import.meta.env.PROD) return () => {};
  void checkForUpdate();

  const onVisibility = (): void => {
    if (document.visibilityState === 'hidden') {
      // The reader is leaving: a notice waits no longer, and the reload costs nothing they see.
      if (update.pending) reloadNow();
      return;
    }
    void checkForUpdate();
  };
  const onCatchUp = (): void => {
    void checkForUpdate();
  };
  const onPageShow = (event: PageTransitionEvent): void => {
    // A page restored from the back/forward cache is stale by definition.
    if (event.persisted) void checkForUpdate();
  };
  const timer = window.setInterval(() => {
    if (document.visibilityState === 'visible') void checkForUpdate();
  }, CHECK_MS);

  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('focus', onCatchUp);
  window.addEventListener('online', onCatchUp);
  window.addEventListener('pageshow', onPageShow);

  return () => {
    window.clearInterval(timer);
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('focus', onCatchUp);
    window.removeEventListener('online', onCatchUp);
    window.removeEventListener('pageshow', onPageShow);
  };
}
