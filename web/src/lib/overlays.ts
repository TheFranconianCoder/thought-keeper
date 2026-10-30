import { navigate, router, withQuery, withoutQuery } from './router.svelte';

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
