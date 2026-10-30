/** Hash routing. The URL is the truth about what the reader is looking at.
 *
 *  Routes:
 *    #/home                   the Start lens (the default)
 *    #/claims                 the claim catalogue
 *    #/claims/<id>            one claim
 *    #/claims?topic=<topic>   the catalogue filtered by topic
 *    #/wiki, #/wiki/<id>, #/wiki/new
 *    #/sources, #/sources/<hash>?tab=…&part=…&file=…
 *    #/attention, #/bookmarks, #/ask, #/ask/<id>, #/status
 *
 *  Overlays are query parameters on the current route (`search=1`, `add=1`), so closing one never
 *  loses the reader's place.
 */

import { beginRouteChange } from './scroll-memory';

export type View =
  | 'home'
  | 'claims'
  | 'wiki'
  | 'sources'
  | 'source'
  | 'attention'
  | 'bookmarks'
  | 'ask'
  | 'status';

const VIEWS = new Set<string>([
  'home',
  'claims',
  'wiki',
  'sources',
  'attention',
  'bookmarks',
  'ask',
  'status',
]);

export interface Route {
  view: View;
  id: string;
  params: URLSearchParams;
  hash: string;
  /** A stable identity for scroll memory and view effects. */
  key: string;
}

export function parseHash(hash: string): Route {
  const raw = (hash || '#/home').replace(/^#\/?/, '');
  const queryAt = raw.indexOf('?');
  const path = queryAt >= 0 ? raw.slice(0, queryAt) : raw;
  const query = queryAt >= 0 ? raw.slice(queryAt + 1) : '';
  const segments = path.split('/').filter(Boolean);
  let view = segments[0] ?? 'home';
  let id = segments[1] ?? '';

  if (view === 'sources' && id) view = 'source';
  if (!VIEWS.has(view) && view !== 'source') {
    view = 'home';
    id = '';
  }

  const params = new URLSearchParams(query);
  return {
    view: view as View,
    id,
    params,
    hash,
    key: `${view}|${id}|${params.toString()}`,
  };
}

const initialHash = typeof location === 'undefined' ? '#/home' : location.hash;
export const router = $state<{ current: Route }>({ current: parseHash(initialHash) });

/** previousKey is the route the current one was entered from, within this session. It lets a detail
 *  view answer "is there a real back?" without guessing from history.length (which includes the
 *  pages before the app). */
let previousKey = '';

let installed = false;

/** startRouter installs the one hashchange listener and returns a cleanup function. */
export function startRouter(): () => void {
  if (installed) return () => {};
  installed = true;
  const sync = (): void => {
    beginRouteChange();
    const next = parseHash(location.hash);
    if (next.key !== router.current.key) previousKey = router.current.key;
    router.current = next;
  };
  window.addEventListener('hashchange', sync);
  window.addEventListener('popstate', sync);
  return () => {
    installed = false;
    window.removeEventListener('hashchange', sync);
    window.removeEventListener('popstate', sync);
  };
}

/** navigate changes the address. A route that is already current is re-evaluated without creating a
 *  second history entry — the same contract as clicking the active nav item. */
export function navigate(hash: string): void {
  if (location.hash === hash) {
    router.current = parseHash(hash);
    return;
  }
  beginRouteChange();
  location.hash = hash;
}

/** previousView names the view the current route was entered from (empty on a cold start). */
export function previousView(): string {
  return previousKey.split('|')[0] ?? '';
}

export function buildHash(view: View | 'sources', id = '', params?: URLSearchParams): string {
  const path = view === 'source' ? 'sources' : view;
  const query = params?.toString() ?? '';
  return `#/${path}${id ? `/${encodeURIComponent(id)}` : ''}${query ? `?${query}` : ''}`;
}

// claimPath addresses one claim. `topic` keeps the LIST's filter in the URL, so opening a claim from
// a filtered list leaves that list filtered (and the browser's back button returns to it).
export function claimPath(id: string, topic = ''): string {
  const base = `#/claims/${encodeURIComponent(id)}`;
  return topic ? `${base}?topic=${encodeURIComponent(topic)}` : base;
}

export function claimTopicPath(topic: string): string {
  return topic ? `#/claims?topic=${encodeURIComponent(topic)}` : '#/claims';
}

export function wikiPath(id?: string): string {
  return `#/wiki${id ? `/${encodeURIComponent(id)}` : ''}`;
}

export function askPath(id?: string): string {
  return `#/ask${id ? `/${encodeURIComponent(id)}` : ''}`;
}

/** askSourcePath opens Ask with the composer scoped to ONE source. */
export function askSourcePath(hash: string): string {
  return `#/ask?source=${encodeURIComponent(hash)}`;
}

export interface SourceLinkOptions {
  tab?: string;
  part?: string;
  file?: string;
}

export function sourcePath(hash: string, options: SourceLinkOptions = {}): string {
  const params = new URLSearchParams();
  if (options.tab) params.set('tab', options.tab);
  if (options.part) params.set('part', options.part);
  if (options.file) params.set('file', options.file);
  const query = params.toString();
  return `#/sources/${encodeURIComponent(hash)}${query ? `?${query}` : ''}`;
}

/** withQuery sets (or, for an empty value, removes) one parameter on the current address. */
export function withQuery(name: string, value: string): string {
  const { view, id, params } = router.current;
  if (value) params.set(name, value);
  else params.delete(name);
  return buildHash(view, id, params);
}

/** withoutQuery removes parameters while keeping the route and every other parameter. */
export function withoutQuery(...names: string[]): string {
  const { view, id, params } = router.current;
  for (const name of names) params.delete(name);
  return buildHash(view, id, params);
}

export function param(name: string): string {
  return router.current.params.get(name) ?? '';
}
