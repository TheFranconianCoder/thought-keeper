import { afterEach, describe, expect, it } from 'vitest';
import { fmtBytes } from '../format';
import { n, setLocale, t, tn } from './index.svelte';

afterEach(() => setLocale('en', false));

describe('number localization', () => {
  it('groups counts the way the interface language does', () => {
    setLocale('en', false);
    expect(n(1_234_567)).toBe('1,234,567');
    expect(tn('claims.captionAll', 1_234)).toBe('1,234 claims');

    setLocale('de', false);
    expect(n(1_234_567)).toBe('1.234.567');
    expect(tn('claims.captionAll', 1_234)).toBe('1.234 Aussagen');
  });

  it('formats numbers inside a sentence, but not an identifier', () => {
    setLocale('de', false);
    expect(t('status.vocabTop', { pct: 1_234 })).toBe('Top 1.234%');
    // A PID is an identifier, not a quantity: it is passed as a string and stays ungrouped.
    expect(t('status.pid', { pid: String(12_345) })).toBe('PID 12345');
  });

  it('writes a byte size with the language decimal separator', () => {
    setLocale('en', false);
    expect(fmtBytes(1_536)).toBe('1.5 KiB');

    setLocale('de', false);
    expect(fmtBytes(1_536)).toBe('1,5 KiB');
  });
});
