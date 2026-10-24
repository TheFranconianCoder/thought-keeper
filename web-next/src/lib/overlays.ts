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
