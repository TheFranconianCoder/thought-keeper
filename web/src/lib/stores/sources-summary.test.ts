import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Source } from '../api/types';
import { sources } from './sources.svelte';

// The store reads a source in one sequenced pass: meta, tree, atoms, log, raw. The stub answers all
// of them so a test can arrive at "the source is open and its summary is on screen".
const originalFetch = globalThis.fetch;

interface SummaryRoute {
  status: number;
  body: string;
  // release lets a test hold one summary read in flight while it switches source.
  hold?: Promise<void>;
}

function stubStore(summary: SummaryRoute) {
  globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://localhost');
    const path = url.pathname;
    if (path.endsWith('/summary')) {
      if (summary.hold) await summary.hold;
      return new Response(summary.body, { status: summary.status });
    }
    if (path.endsWith('/tree')) return new Response(JSON.stringify({ nodes: [] }), { status: 200 });
    if (path.endsWith('/atoms')) return new Response(JSON.stringify({ atoms: [], total: 0 }), { status: 200 });
    if (path.endsWith('/log')) return new Response('', { status: 200 });
    if (path.endsWith('/raw')) return new Response('', { status: 200 });
    const hash = path.split('/').pop() ?? '';
    return new Response(
      JSON.stringify({ hash, kind: 'url', ext: '.md', title: hash, steps: {} } as Source),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    );
  }) as typeof fetch;
}

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('source summary', () => {
  it('keeps the summary on screen when a re-read fails', async () => {
    stubStore({ status: 200, body: 'A summary that is already on screen.' });
    await sources.open('summary-test-a');
    await sources.loadSummary('summary-test-a', true);
    expect(sources.summary).toBe('A summary that is already on screen.');

    // The next read fails (a timeout on a remote instance). The panel must not flash empty.
    stubStore({ status: 500, body: 'boom' });
    await sources.loadSummary('summary-test-a', true);
    expect(sources.summary).toBe('A summary that is already on screen.');
    expect(sources.summaryError).not.toBe('');
    expect(sources.summaryMissing).toBe(false);
  });

  it('shows the empty state on a 404 and clears the stale text', async () => {
    stubStore({ status: 200, body: 'Old text.' });
    await sources.open('summary-test-b');
    await sources.loadSummary('summary-test-b', true);
    expect(sources.summary).toBe('Old text.');

    stubStore({ status: 404, body: '' });
    await sources.loadSummary('summary-test-b', true);
    expect(sources.summary).toBe('');
    expect(sources.summaryMissing).toBe(true);
    expect(sources.summaryError).toBe('');
  });

  it('does not paint the summary of a source that is no longer open', async () => {
    let release: () => void = () => {};
    const hold = new Promise<void>((resolve) => (release = resolve));
    stubStore({ status: 200, body: 'Summary of the first source.', hold });
    await sources.open('summary-test-c');
    const pending = sources.loadSummary('summary-test-c', true);

    // The reader moves on: another source opens while the first read is still in flight.
    stubStore({ status: 404, body: '' });
    await sources.open('summary-test-d');
    const before = sources.summary;
    release();
    await pending;
    expect(sources.summary).toBe(before);
  });
});
