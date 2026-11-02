import { afterEach, describe, expect, it } from 'vitest';
import { criteriaChips, windowLabel } from './assistant';
import { setLocale } from './i18n/index.svelte';

afterEach(() => setLocale('en', false));

describe('assistant window', () => {
  it('renders calendar and rolling specs', () => {
    setLocale('en', false);
    expect(windowLabel('yesterday')).toBe('yesterday');
    expect(windowLabel('last-7d')).toBe('in the last 7 days');
    expect(windowLabel('last-2m')).toBe('in the last 2 months');
    expect(windowLabel('2026-10-01..2026-10-31')).toContain('Oct');
  });

  it('names the verb a lens uses for its clock', () => {
    setLocale('en', false);
    expect(criteriaChips({ window: { spec: 'yesterday' } }, 'sources')[0].text).toBe('added yesterday');
    expect(criteriaChips({ window: { spec: 'yesterday' } }, 'claims')[0].text).toBe('created yesterday');
    expect(criteriaChips({ window: { spec: 'yesterday' } }, 'wiki')[0].text).toBe('edited yesterday');
  });
});
