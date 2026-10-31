import { buildHash, navigate, router, withQuery, withoutQuery } from './router.svelte';

export function searchOpen(): boolean {
  return router.current.params.get('search') === '1';
}

export function addOpen(): boolean {
  return router.current.params.get('add') === '1';
}

export function openSearch(): void {
  navigate(withQuery('search', '1'));
}

export function closeSearch(): void {
  navigate(withoutQuery('search'));
}

export function openAdd(): void {
  navigate(withQuery('add', '1'));
}

export function closeAdd(): void {
  navigate(withoutQuery('add'));
}

/** The interests manager is an overlay too: it is a rule book, not a lens. Opening it from the
 *  Start card, the queue banner or the Sources filter row never loses the reader's place. */
export function interestsOpen(): boolean {
  return router.current.params.get('interests') === '1';
}

/** openInterests opens the manager. A seed (a source hash) prefills the editor from that source's
 *  topics and labels — never free text, so the address stays a name and not a sentence. */
export function openInterests(seed = ''): void {
  const params = new URLSearchParams(router.current.params);
  params.set('interests', '1');
  if (seed) params.set('seed', seed);
  else params.delete('seed');
  navigate(buildHash(router.current.view, router.current.id, params));
}

export function closeInterests(): void {
  navigate(withoutQuery('interests', 'seed'));
}

/** The assistant's field is an overlay on the current route, like search and add: closing it never
 *  loses the reader's place. */
export function assistantOpen(): boolean {
  return router.current.params.get('assistant') === '1';
}

export function openAssistant(): void {
  navigate(withQuery('assistant', '1'));
}

export function closeAssistant(): void {
  navigate(withoutQuery('assistant'));
}
