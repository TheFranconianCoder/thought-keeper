import { afterEach, describe, expect, it, vi } from 'vitest';

// The store's only API calls are the acknowledgment and the resync read; mock them so the phase
// machine can be exercised without a server.
vi.mock('./api', () => ({
  api: { readerActive: vi.fn(), reader: vi.fn() },
}));

import { api } from './api';
import {
  activateNewSources,
  applySharedSeen,
  arrivalWindowExtended,
  arrivalWindowStart,
  isNew,
  isSourceNew,
  isSourceUpdated,
  markSourceOpened,
  newSources,
} from './new-sources.svelte';

const DAY = 86_400_000;
const NOW = Date.parse('2026-11-07T12:00:00Z');

describe('isNew', () => {
  it('marks an arrival after the baseline', () => {
    expect(isNew('2026-11-07T08:00:01Z', '2026-11-07T08:00:00Z')).toBe(true);
  });

  it('does not mark the baseline itself or anything older', () => {
    expect(isNew('2026-11-07T08:00:00Z', '2026-11-07T08:00:00Z')).toBe(false);
    expect(isNew('2026-11-07T07:59:59Z', '2026-11-07T08:00:00Z')).toBe(false);
  });

  it('marks nothing without a baseline', () => {
    expect(isNew('2026-11-07T08:00:00Z', '')).toBe(false);
    expect(isNew(undefined, '2026-11-07T08:00:00Z')).toBe(false);
  });
});

describe('opening a source', () => {
  afterEach(() => {
    newSources.since = '';
    newSources.opened = [];
  });

  it('drops the mark of the source the reader opened, and of that source only', () => {
    newSources.since = '2026-11-07T08:00:00Z';
    const opened = { hash: 'aa', imported_at: '2026-11-07T09:00:00Z' };
    const other = { hash: 'bb', imported_at: '2026-11-07T09:00:00Z' };
    expect(isSourceNew(opened)).toBe(true);
    markSourceOpened(opened.hash);
    expect(isSourceNew(opened)).toBe(false);
    expect(isSourceNew(other)).toBe(true);
  });

  it('marks nothing without a baseline, opened or not', () => {
    const source = { hash: 'cc', imported_at: '2026-11-07T09:00:00Z' };
    expect(isSourceNew(source)).toBe(false);
    markSourceOpened(source.hash);
    expect(isSourceNew(source)).toBe(false);
  });

  it('ignores an empty hash and the same source twice', () => {
    markSourceOpened('dd');
    markSourceOpened('dd');
    markSourceOpened('');
    expect(newSources.opened).toEqual(['dd']);
  });
});

describe('the updated mark', () => {
  afterEach(() => {
    newSources.since = '';
    newSources.opened = [];
  });

  it('marks content that changed after the baseline', () => {
    newSources.since = '2026-11-07T08:00:00Z';
    const source = {
      hash: 'up',
      imported_at: '2026-10-01T08:00:00Z',
      url_changed_at: '2026-11-07T09:00:00Z',
    };
    expect(isSourceUpdated(source)).toBe(true);
  });

  it('does not mark a source that merely arrived in this batch', () => {
    newSources.since = '2026-11-07T08:00:00Z';
    const source = {
      hash: 'new',
      imported_at: '2026-11-07T09:00:00Z',
      url_changed_at: '2026-11-07T09:00:00Z',
    };
    expect(isSourceUpdated(source)).toBe(false);
  });

  it('marks nothing without a change time or a baseline', () => {
    newSources.since = '2026-11-07T08:00:00Z';
    expect(isSourceUpdated({ hash: 'a', imported_at: '2026-10-01T08:00:00Z' })).toBe(false);
    newSources.since = '';
    expect(
      isSourceUpdated({
        hash: 'b',
        imported_at: '2026-10-01T08:00:00Z',
        url_changed_at: '2026-11-07T09:00:00Z',
      }),
    ).toBe(false);
  });

  it('drops the mark for a source the reader opened', () => {
    newSources.since = '2026-11-07T08:00:00Z';
    const source = {
      hash: 'opened',
      imported_at: '2026-10-01T08:00:00Z',
      url_changed_at: '2026-11-07T09:00:00Z',
    };
    expect(isSourceUpdated(source)).toBe(true);
    markSourceOpened(source.hash);
    expect(isSourceUpdated(source)).toBe(false);
  });
});

describe('the Start window', () => {
  it('keeps three days when the last visit is recent', () => {
    expect(arrivalWindowStart(NOW, '2026-11-07T08:00:00Z')).toBe(
      new Date(NOW - 3 * DAY).toISOString(),
    );
    expect(arrivalWindowExtended(NOW, '2026-11-07T08:00:00Z')).toBe(false);
  });

  it('reaches back to the last visit when it is older than three days', () => {
    const since = '2026-11-01T08:00:00Z';
    expect(Date.parse(arrivalWindowStart(NOW, since))).toBe(Date.parse(since));
    expect(arrivalWindowExtended(NOW, since)).toBe(true);
  });

  it('falls back to three days without a baseline', () => {
    expect(arrivalWindowStart(NOW, '')).toBe(new Date(NOW - 3 * DAY).toISOString());
    expect(arrivalWindowExtended(NOW, '')).toBe(false);
  });
});

describe('the shared watermark', () => {
  afterEach(() => {
    newSources.since = '';
    newSources.seenAt = '';
    newSources.active = false;
    newSources.ack = 'idle';
    newSources.primary = false;
    vi.clearAllMocks();
  });

  it('clears the marks when another client moved it', () => {
    newSources.since = '2026-11-07T07:00:00Z';
    newSources.seenAt = '2026-11-07T07:00:00Z';
    applySharedSeen('2026-11-07T09:00:00Z');
    expect(newSources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.since).toBe('2026-11-07T09:00:00Z');
  });

  it('ignores an older frame, so a late arrival cannot re-open what was seen', () => {
    newSources.since = '2026-11-07T09:00:00Z';
    newSources.seenAt = '2026-11-07T09:00:00Z';
    applySharedSeen('2026-11-07T08:00:00Z');
    expect(newSources.since).toBe('2026-11-07T09:00:00Z');
  });
});

describe('acknowledging a phase', () => {
  function phase(since = '', seenAt = ''): void {
    newSources.primary = true;
    newSources.active = true;
    newSources.ack = 'idle';
    newSources.since = since;
    newSources.seenAt = seenAt;
  }

  afterEach(() => {
    newSources.since = '';
    newSources.seenAt = '';
    newSources.active = false;
    newSources.ack = 'idle';
    newSources.primary = false;
    vi.clearAllMocks();
  });

  it('marks from the server\'s previous watermark and keeps the new one', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateNewSources();
    expect(newSources.since).toBe('2026-11-07T07:00:00Z');
    expect(newSources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.ack).toBe('done');
  });

  it('marks nothing on the very first acknowledgment', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({ seen_at: '2026-11-07T09:00:00Z', first: true });
    await activateNewSources();
    expect(newSources.since).toBe('');
    expect(newSources.seenAt).toBe('2026-11-07T09:00:00Z');
  });

  it('never re-opens a newer watermark another client set while the call was in flight', async () => {
    phase('2026-11-07T10:00:00Z', '2026-11-07T10:00:00Z');
    vi.mocked(api.readerActive).mockResolvedValue({
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateNewSources();
    expect(newSources.since).toBe('2026-11-07T10:00:00Z');
    expect(newSources.seenAt).toBe('2026-11-07T10:00:00Z');
  });

  it('keeps the last known baseline when the server cannot acknowledge', async () => {
    phase('2026-11-07T09:00:00Z', '2026-11-07T09:00:00Z');
    vi.mocked(api.readerActive).mockRejectedValue(new Error('repairing'));
    await activateNewSources();
    expect(newSources.since).toBe('2026-11-07T09:00:00Z');
    expect(newSources.ack).toBe('failed');
  });

  it('never acknowledges from a duplicate window or twice in one phase', async () => {
    newSources.active = true;
    newSources.ack = 'idle';
    newSources.primary = false;
    await activateNewSources();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();

    newSources.primary = true;
    newSources.ack = 'done';
    await activateNewSources();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();
  });
});
