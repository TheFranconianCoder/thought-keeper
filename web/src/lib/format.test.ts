import { describe, expect, it } from 'vitest';
import { pubDate } from './format';

describe('pubDate', () => {
  it('keeps a date without a time on its own day', () => {
    // A date-only value is stored at midnight UTC. Rendering it in a zone west of UTC would show
    // the previous day, so the value is shown in UTC — the day the source declared.
    // The web project carries no node types; vitest still runs on node.
    const env = (globalThis as unknown as { process: { env: Record<string, string | undefined> } }).process.env;
    const tz = env.TZ;
    env.TZ = 'America/New_York';
    try {
      expect(pubDate('2026-10-27T00:00:00Z')).toContain('27');
      expect(pubDate('2026-10-27T00:00:00Z')).toContain('2026');
    } finally {
      env.TZ = tz;
    }
  });

  it('is empty without a date', () => {
    expect(pubDate(undefined)).toBe('');
    expect(pubDate('')).toBe('');
  });
});
