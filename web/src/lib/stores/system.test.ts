import { afterEach, describe, expect, it, vi } from 'vitest';
import { system } from './system.svelte';

// The doctor report and the vault report are asked for twice when the Status lens opens: once by
// the lens itself and once by the catch-up that every start runs. A second request is fine (the
// catch-up wants the latest), a second SCAN side by side is not — the two cold reports are what made
// the page wait, so the store joins them.
const originalFetch = globalThis.fetch;

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe('system store', () => {
  it('never runs two doctor-report reads at once', async () => {
    let live = 0;
    let peak = 0;
    let calls = 0;
    globalThis.fetch = vi.fn(async () => {
      calls++;
      live++;
      peak = Math.max(peak, live);
      await Promise.resolve();
      live--;
      return json({ version: 'test-version' });
    }) as typeof fetch;

    await Promise.all([system.loadStatus(), system.loadStatus()]);

    expect(peak).toBe(1);
    // The second ask is a trailing read, not a competing one.
    expect(calls).toBe(2);
    expect(system.status?.version).toBe('test-version');
    expect(system.statusError).toBe('');
  });

  it('joins two vault reads into one scan per report', async () => {
    const calls = new Map<string, number>();
    globalThis.fetch = vi.fn(async (input: RequestInfo | URL) => {
      const path = new URL(String(input), 'http://localhost').pathname;
      calls.set(path, (calls.get(path) ?? 0) + 1);
      await Promise.resolve();
      if (path.endsWith('/vocabulary')) return json({ namespaces: [] });
      return json({ groups: 0, files: 0, collapsed: 0 });
    }) as typeof fetch;

    await Promise.all([system.loadVault(), system.loadVault()]);

    expect(calls.get('/api/v1/atoms/duplicates')).toBe(1);
    expect(calls.get('/api/v1/vocabulary')).toBe(1);
    expect(system.vaultLoaded).toBe(true);
  });

  it('keeps the last report when a read fails, and says why', async () => {
    globalThis.fetch = vi.fn(async () => json({ version: 'served' })) as typeof fetch;
    await system.loadStatus();
    expect(system.status?.version).toBe('served');

    globalThis.fetch = vi.fn(async () => new Response('{"error":{"code":"internal","message":"boom"}}', { status: 500 })) as typeof fetch;
    await system.loadStatus();

    expect(system.status?.version).toBe('served');
    expect(system.statusError).toContain('boom');
  });
});
