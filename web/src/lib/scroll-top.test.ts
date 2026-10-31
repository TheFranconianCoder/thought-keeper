import { describe, expect, it } from 'vitest';
import { furthestScrolled, SCROLL_TOP_THRESHOLD } from './scroll-top.svelte';

describe('furthestScrolled', () => {
  it('stays quiet while every list is near its first row', () => {
    expect(furthestScrolled([0, 120, SCROLL_TOP_THRESHOLD - 1])).toBe(-1);
  });

  it('names the one list that scrolled past the threshold', () => {
    expect(furthestScrolled([0, SCROLL_TOP_THRESHOLD + 1])).toBe(1);
  });

  it('names the furthest when two lists are scrolled', () => {
    expect(furthestScrolled([900, 0, 1400, 700])).toBe(2);
  });

  it('needs to be strictly past the threshold', () => {
    expect(furthestScrolled([SCROLL_TOP_THRESHOLD])).toBe(-1);
    expect(furthestScrolled([0], 0)).toBe(-1);
  });

  it('has nothing to name without containers', () => {
    expect(furthestScrolled([])).toBe(-1);
  });
});
