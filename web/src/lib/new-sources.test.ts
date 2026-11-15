import { afterEach, describe, expect, it, vi } from 'vitest';

// The store's only API calls are the acknowledgment and the resync read; mock them so the phase
// machine can be exercised without a server.
vi.mock('./api', () => ({
  api: { readerActive: vi.fn(), reader: vi.fn() },
}));

import { api } from './api';
import {
  activateSources,
  activateStart,
  applySharedSeen,
  isNew,
  isSourceNew,
  isSourceUpdated,
  markSourceOpened,
  newSources,
  refreshNewSources,
} from './new-sources.svelte';

const reset = (): void => {
  newSources.start = { since: '', seenAt: '', ack: 'idle' };
  newSources.sources = { since: '', seenAt: '', ack: 'idle' };
  newSources.active = false;
  newSources.primary = false;
  newSources.opened = [];
};

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

describe('the Sources marks', () => {
  afterEach(reset);

  it('drops the mark of the source the reader opened, and of that source only', () => {
    newSources.sources.since = '2026-11-07T08:00:00Z';
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

  it('does not use the Start baseline for a row', () => {
    newSources.start.since = '2026-11-07T08:00:00Z';
    newSources.sources.since = '';
    expect(isSourceNew({ hash: 'ee', imported_at: '2026-11-07T09:00:00Z' })).toBe(false);
  });
});

describe('the updated mark', () => {
  afterEach(reset);

  it('marks content that changed after the Sources baseline', () => {
    newSources.sources.since = '2026-11-07T08:00:00Z';
    const source = {
      hash: 'up',
      imported_at: '2026-10-01T08:00:00Z',
      url_changed_at: '2026-11-07T09:00:00Z',
    };
    expect(isSourceUpdated(source)).toBe(true);
  });

  it('does not mark a source that merely arrived in this batch', () => {
    newSources.sources.since = '2026-11-07T08:00:00Z';
    const source = {
      hash: 'new',
      imported_at: '2026-11-07T09:00:00Z',
      url_changed_at: '2026-11-07T09:00:00Z',
    };
    expect(isSourceUpdated(source)).toBe(false);
  });

  it('marks nothing without a change time or a baseline', () => {
    newSources.sources.since = '2026-11-07T08:00:00Z';
    expect(isSourceUpdated({ hash: 'a', imported_at: '2026-10-01T08:00:00Z' })).toBe(false);
    newSources.sources.since = '';
    expect(
      isSourceUpdated({
        hash: 'b',
        imported_at: '2026-10-01T08:00:00Z',
        url_changed_at: '2026-11-07T09:00:00Z',
      }),
    ).toBe(false);
  });

  it('drops the mark for a source the reader opened', () => {
    newSources.sources.since = '2026-11-07T08:00:00Z';
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

describe('the shared watermarks', () => {
  afterEach(() => {
    reset();
    vi.clearAllMocks();
  });

  it('clears the marks of the scope another client moved', () => {
    newSources.sources.since = '2026-11-07T07:00:00Z';
    newSources.sources.seenAt = '2026-11-07T07:00:00Z';
    applySharedSeen('sources', '2026-11-07T09:00:00Z');
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
  });

  it('leaves the other scope untouched', () => {
    newSources.start.since = '2026-11-07T07:00:00Z';
    newSources.start.seenAt = '2026-11-07T07:00:00Z';
    applySharedSeen('sources', '2026-11-07T09:00:00Z');
    expect(newSources.start.seenAt).toBe('2026-11-07T07:00:00Z');
    expect(newSources.start.since).toBe('2026-11-07T07:00:00Z');
  });

  it('ignores an older frame, so a late arrival cannot re-open what was seen', () => {
    newSources.sources.since = '2026-11-07T09:00:00Z';
    newSources.sources.seenAt = '2026-11-07T09:00:00Z';
    applySharedSeen('sources', '2026-11-07T08:00:00Z');
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
  });

  it('applies both scopes on a resync', async () => {
    vi.mocked(api.reader).mockResolvedValue({
      start_seen_at: '2026-11-07T08:00:00Z',
      sources_seen_at: '2026-11-07T09:00:00Z',
    });
    await refreshNewSources();
    expect(newSources.start.seenAt).toBe('2026-11-07T08:00:00Z');
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
  });
});

describe('acknowledging a phase', () => {
  function phase(): void {
    reset();
    newSources.primary = true;
    newSources.active = true;
  }

  afterEach(() => {
    reset();
    vi.clearAllMocks();
  });

  it('acknowledges Start and keeps the previous watermark as its baseline', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'start',
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateStart();
    expect(vi.mocked(api.readerActive)).toHaveBeenCalledWith('start', expect.anything());
    expect(newSources.start.since).toBe('2026-11-07T07:00:00Z');
    expect(newSources.start.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.start.ack).toBe('done');
  });

  it('acknowledges Sources on its own baseline, without touching Start', async () => {
    phase();
    newSources.start.since = '2026-11-07T05:00:00Z';
    newSources.start.seenAt = '2026-11-07T06:00:00Z';
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'sources',
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateSources();
    expect(newSources.sources.since).toBe('2026-11-07T07:00:00Z');
    expect(newSources.start.since).toBe('2026-11-07T05:00:00Z');
    expect(newSources.start.ack).toBe('idle');
  });

  it('marks nothing on a scope\'s very first acknowledgment', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'sources',
      seen_at: '2026-11-07T09:00:00Z',
      first: true,
    });
    await activateSources();
    expect(newSources.sources.since).toBe('');
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
  });

  it('never re-opens a newer watermark another client set while the call was in flight', async () => {
    phase();
    newSources.sources.since = '2026-11-07T10:00:00Z';
    newSources.sources.seenAt = '2026-11-07T10:00:00Z';
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'sources',
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateSources();
    expect(newSources.sources.since).toBe('2026-11-07T10:00:00Z');
    expect(newSources.sources.seenAt).toBe('2026-11-07T10:00:00Z');
  });

  it('keeps the last known baseline when the server cannot acknowledge', async () => {
    phase();
    newSources.start.since = '2026-11-07T09:00:00Z';
    newSources.start.seenAt = '2026-11-07T09:00:00Z';
    vi.mocked(api.readerActive).mockRejectedValue(new Error('repairing'));
    await activateStart();
    expect(newSources.start.since).toBe('2026-11-07T09:00:00Z');
    expect(newSources.start.ack).toBe('failed');
  });

  it('never acknowledges from a duplicate window or twice in one phase', async () => {
    newSources.active = true;
    newSources.primary = false;
    await activateStart();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();

    newSources.primary = true;
    newSources.start.ack = 'done';
    await activateStart();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();
  });

  it('acknowledges both lenses once each in the same phase', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'start',
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateStart();
    await activateSources();
    expect(vi.mocked(api.readerActive)).toHaveBeenCalledTimes(2);
  });
});
