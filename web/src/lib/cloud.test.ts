import { describe, expect, it } from 'vitest';
import { CLOUD_LIMIT, CLOUD_THRESHOLD, visibleCloud } from './cloud';

interface Item {
  name: string;
  count: number;
}

const key = (item: Item) => item.name;
const count = (item: Item) => item.count;

function items(n: number, counter: (index: number) => number = (index) => n - index): Item[] {
  return Array.from({ length: n }, (_, index) => ({ name: `tag-${index}`, count: counter(index) }));
}

function selection(source: Item[], expanded = false, active = '') {
  return visibleCloud(source, { expanded, active, key, count });
}

describe('visibleCloud', () => {
  it('leaves a small cloud completely alone', () => {
    const result = selection(items(CLOUD_THRESHOLD));
    expect(result.visible).toHaveLength(CLOUD_THRESHOLD);
    expect(result.hidden).toBe(0);
    expect(result.truncated).toBe(false);
  });

  it('keeps the head and folds the rest', () => {
    const result = selection(items(CLOUD_THRESHOLD + 10));
    expect(result.visible).toHaveLength(CLOUD_LIMIT);
    expect(result.hidden).toBe(CLOUD_THRESHOLD + 10 - CLOUD_LIMIT);
    expect(result.truncated).toBe(true);
  });

  it('orders by use, then by name', () => {
    const source = [
      { name: 'b', count: 3 },
      { name: 'a', count: 3 },
      { name: 'c', count: 9 },
    ];
    // A cloud this small is not truncated; the helper preserves the given order.
    expect(selection(source).visible.map(key)).toEqual(['b', 'a', 'c']);

    const large = items(30, (index) => (index < 3 ? 5 : 1));
    expect(selection(large).visible.slice(0, 3).map(key)).toEqual(['tag-0', 'tag-1', 'tag-2']);
  });

  it('shows everything when expanded', () => {
    const source = items(CLOUD_THRESHOLD + 10);
    const result = selection(source, true);
    expect(result.visible).toHaveLength(source.length);
    expect(result.hidden).toBe(0);
    expect(result.truncated).toBe(true);
  });

  it('keeps the active filter visible even in the tail', () => {
    const source = items(CLOUD_THRESHOLD + 10, () => 1);
    const last = source[source.length - 1].name;
    const result = selection(source, false, last);
    expect(result.visible.map(key)).toContain(last);
    expect(result.visible).toHaveLength(CLOUD_LIMIT + 1);
    expect(result.hidden).toBe(source.length - CLOUD_LIMIT - 1);
  });

  it('does not duplicate an active filter that is already in the head', () => {
    const source = items(CLOUD_THRESHOLD + 10);
    const head = source[0].name;
    const result = selection(source, false, head);
    expect(result.visible.filter((item) => item.name === head)).toHaveLength(1);
    expect(result.hidden).toBe(source.length - CLOUD_LIMIT);
  });

  it('does not mutate the source', () => {
    const source = items(CLOUD_THRESHOLD + 10);
    const before = source.map(key);
    selection(source);
    expect(source.map(key)).toEqual(before);
  });

  it('needs more than the threshold, not exactly the threshold', () => {
    expect(selection(items(CLOUD_THRESHOLD + 1)).truncated).toBe(true);
    expect(selection(items(CLOUD_THRESHOLD)).truncated).toBe(false);
  });
});
