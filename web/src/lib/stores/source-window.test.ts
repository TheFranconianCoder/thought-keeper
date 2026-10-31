import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Source } from '../api/types';
import { sourceWindow } from './source-window.svelte';

const originalFetch = globalThis.fetch;

function stubSourcePages(total = 500) {
  const offsets: number[] = [];
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname.endsWith('/sources/facets')) {
      return new Response(JSON.stringify({ tags: [{ tag: 'go', count: 2 }], attached: 1 }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }
    const offset = Number(url.searchParams.get('offset') ?? 0);
    const limit = Number(url.searchParams.get('limit') ?? 50);
    offsets.push(offset);
    const count = Math.max(0, Math.min(limit, total - offset));
    const sources = Array.from({ length: count }, (_, i) => ({
      hash: `h${offset + i}`,
    })) as Source[];
    return new Response(JSON.stringify({ sources, count: total, total }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as typeof fetch;
  return offsets;
}

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('source window', () => {
  it('reads the requested page and knows the filtered total', async () => {
    const offsets = stubSourcePages(500);
    sourceWindow.setFilter({ query: 'window-test-a' });
    await sourceWindow.ensure(0, 50);

    expect(offsets).toEqual([0]);
    expect(sourceWindow.count).toBe(500);
    expect(sourceWindow.loaded).toBe(true);
    expect(sourceWindow.itemAt(0)?.hash).toBe('h0');
    expect(sourceWindow.itemAt(49)?.hash).toBe('h49');
  });

  it('loads a deep range without loading the rows in between and evicts far pages', async () => {
    const offsets = stubSourcePages(5000);
    sourceWindow.setFilter({ query: 'window-test-b' });
    await sourceWindow.ensure(0, 50);
    await sourceWindow.ensure(1000, 1050);

    expect(offsets).toContain(1000);
    expect(sourceWindow.itemAt(1000)?.hash).toBe('h1000');
    // The first page is not kept forever: the window is a cache around the viewport.
    expect(sourceWindow.itemAt(0)).toBeUndefined();
  });

  it('keeps the loaded rows on screen while an invalidation re-reads them', async () => {
    stubSourcePages(500);
    sourceWindow.setFilter({ query: 'window-test-c' });
    await sourceWindow.ensure(0, 50);
    const before = sourceWindow.itemAt(0)?.hash;
    expect(before).toBe('h0');

    sourceWindow.invalidate();
    // The event must not blank the window: the old page is still there until the fresh one arrives.
    expect(sourceWindow.itemAt(0)?.hash).toBe(before);
    expect(sourceWindow.loaded).toBe(true);
    expect(sourceWindow.loading).toBe(false);

    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(sourceWindow.itemAt(0)?.hash).toBe(before);
  });

  it('reads the facets separately from the rows', async () => {
    stubSourcePages(10);
    sourceWindow.invalidate();
    await sourceWindow.loadFacets();

    expect(sourceWindow.facets.tags).toEqual([{ tag: 'go', count: 2 }]);
    expect(sourceWindow.facets.attached).toBe(1);
  });
});
