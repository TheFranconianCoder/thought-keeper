import { describe, expect, it } from 'vitest';
import { viewportHeight } from './viewport';

describe('viewportHeight', () => {
  it('floors the visible height so nothing is left outside the window', () => {
    expect(viewportHeight({ height: 784.9 }, 800)).toBe(784);
  });

  it('ignores a pinch-zoom, which magnifies rather than resizes the app', () => {
    expect(viewportHeight({ height: 400, scale: 2 }, 800)).toBe(0);
    expect(viewportHeight({ height: 784, scale: 1 }, 800)).toBe(784);
  });

  it('falls back to the window height when the visual viewport is missing or unusable', () => {
    expect(viewportHeight(null, 812.4)).toBe(812);
    expect(viewportHeight({ height: 0 }, 812)).toBe(812);
    expect(viewportHeight({ height: Number.NaN }, 812)).toBe(812);
  });

  it('reports nothing when there is no usable reading at all', () => {
    expect(viewportHeight(null, 0)).toBe(0);
    expect(viewportHeight(null, Number.NaN)).toBe(0);
  });
});
