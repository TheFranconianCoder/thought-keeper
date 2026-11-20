import { afterEach, describe, expect, it, vi } from 'vitest';

// The store's only API calls are the acknowledgment and the resync read; mock them so the phase
// machine can be exercised without a server.
vi.mock('./api', () => ({
  api: { readerActive: vi.fn(), reader: vi.fn() },
}));

import { api } from './api';
import {
  activateSources,
  applySharedSeen,
  isNew,
  isSourceNew,
  isSourceUpdated,
  markSourceOpened,
  newSources,
  refreshNewSources,
  retryNewSources,
} from './new-sources.svelte';

const reset = (): void => {
  newSources.sources = { since: '', seenAt: '', ack: 'idle' };
  newSources.active = false;
  newSources.primary = false;
  newSources.opened = [];
  newSources.loaded = false;
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

describe('the row marks', () => {
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
});

describe('the updated mark', () => {
  afterEach(reset);

  it('marks content that changed after the baseline', () => {
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

describe('the shared watermark', () => {
  afterEach(() => {
    reset();
    vi.clearAllMocks();
  });

  it('clears this window\'s marks when another client moves it', () => {
    newSources.sources.since = '2026-11-07T07:00:00Z';
    newSources.sources.seenAt = '2026-11-07T07:00:00Z';
    applySharedSeen('2026-11-07T09:00:00Z');
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
  });

  it('ignores an older frame, so a late arrival cannot re-open what was seen', () => {
    newSources.sources.since = '2026-11-07T09:00:00Z';
    newSources.sources.seenAt = '2026-11-07T09:00:00Z';
    applySharedSeen('2026-11-07T08:00:00Z');
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
  });

  it('applies the resync and names the baseline as known', async () => {
    vi.mocked(api.reader).mockResolvedValue({ sources_seen_at: '2026-11-07T09:00:00Z' });
    await refreshNewSources();
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
    expect(newSources.loaded).toBe(true);
  });

  it('treats an unacknowledged vault as a known, empty baseline', async () => {
    vi.mocked(api.reader).mockResolvedValue({});
    await refreshNewSources();
    expect(newSources.sources.since).toBe('');
    expect(newSources.loaded).toBe(true);
  });

  it('resolves even when the server cannot be read, so the front door is not stuck', async () => {
    vi.mocked(api.reader).mockRejectedValue(new Error('offline'));
    await refreshNewSources();
    expect(newSources.loaded).toBe(true);
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

  it('keeps the previous watermark as its baseline and moves the shared one', async () => {
    phase();
    vi.mocked(api.readerActive).mockResolvedValue({
      scope: 'sources',
      since: '2026-11-07T07:00:00Z',
      seen_at: '2026-11-07T09:00:00Z',
    });
    await activateSources();
    expect(vi.mocked(api.readerActive)).toHaveBeenCalledWith('sources', expect.anything());
    // The rows keep the batch this visit settled for the rest of the phase.
    expect(newSources.sources.since).toBe('2026-11-07T07:00:00Z');
    expect(newSources.sources.seenAt).toBe('2026-11-07T09:00:00Z');
    expect(newSources.sources.ack).toBe('done');
  });

  it('marks nothing on a vault\'s first acknowledgment', async () => {
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
    newSources.sources.since = '2026-11-07T09:00:00Z';
    newSources.sources.seenAt = '2026-11-07T09:00:00Z';
    vi.mocked(api.readerActive).mockRejectedValue(new Error('repairing'));
    await activateSources();
    expect(newSources.sources.since).toBe('2026-11-07T09:00:00Z');
    expect(newSources.sources.ack).toBe('failed');
    retryNewSources();
    expect(newSources.sources.ack).toBe('idle');
  });

  it('never acknowledges from a duplicate window or twice in one phase', async () => {
    newSources.active = true;
    newSources.primary = false;
    await activateSources();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();

    newSources.primary = true;
    newSources.sources.ack = 'done';
    await activateSources();
    expect(vi.mocked(api.readerActive)).not.toHaveBeenCalled();
  });
});
