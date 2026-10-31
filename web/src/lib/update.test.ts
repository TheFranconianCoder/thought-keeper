import { describe, expect, it } from 'vitest';
import { decide, entryOf, isStale } from './update';

const RUNNING = '/assets/index-old.js';
const SERVED = '/assets/index-new.js';

describe('entryOf', () => {
  it('names the path of the running module, not its origin', () => {
    expect(entryOf('http://127.0.0.1:9210/assets/index-abc.js')).toBe('/assets/index-abc.js');
  });

  it('passes an unparseable value through instead of throwing', () => {
    expect(entryOf('/assets/index-abc.js')).toBe('/assets/index-abc.js');
  });
});

describe('isStale', () => {
  it('is quiet when the served build is the running one', () => {
    expect(isStale({ entry: RUNNING }, RUNNING)).toBe(false);
  });

  it('is quiet when the service has no manifest', () => {
    expect(isStale(null, RUNNING)).toBe(false);
    expect(isStale({}, RUNNING)).toBe(false);
  });

  it('reports a different entry chunk', () => {
    expect(isStale({ entry: SERVED }, RUNNING)).toBe(true);
  });
});

describe('decide', () => {
  it('does nothing while the build is current', () => {
    expect(decide({ entry: RUNNING }, RUNNING, { hidden: false })).toBe('none');
    expect(decide({ entry: RUNNING }, RUNNING, { hidden: true })).toBe('none');
  });

  it('reloads a hidden window at once: nobody is typing there', () => {
    expect(decide({ entry: SERVED }, RUNNING, { hidden: true })).toBe('reload');
  });

  it('tells a visible window instead of reloading under the reader', () => {
    expect(decide({ entry: SERVED }, RUNNING, { hidden: false })).toBe('notify');
  });

  it('does not reload twice for the same build', () => {
    expect(decide({ entry: SERVED }, RUNNING, { hidden: true, reloadedFor: SERVED })).toBe('notify');
    expect(decide({ entry: SERVED }, RUNNING, { hidden: false, reloadedFor: SERVED })).toBe('notify');
  });

  it('reloads again for a build it has not tried yet', () => {
    expect(decide({ entry: SERVED }, RUNNING, { hidden: true, reloadedFor: RUNNING })).toBe('reload');
  });
});
