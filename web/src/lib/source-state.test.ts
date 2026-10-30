import { describe, expect, it } from 'vitest';
import type { Pipeline, Source } from './api/types';
import {
  canAttachSnapshot,
  countsOf,
  matchesSourceFilter,
  overallPercent,
  overallState,
  pipelineLabel,
  progressOf,
  statusLabel,
  totalsLabel,
} from './source-state';

function source(patch: Partial<Source> = {}): Source {
  return {
    hash: 'a'.repeat(64),
    kind: 'md',
    ext: '.md',
    path: 'sources/aa/aa/a.md',
    title: 'A source',
    bytes: 1234,
    tree_state: 'built',
    original_name: 'a.md',
    parts_total: 10,
    parts_done: 10,
    atoms_left: 0,
    atom_count: 5,
    provenance_rows: 5,
    imported_at: '2026-01-01T00:00:00Z',
    steps: {
      capture: { state: 'ok' },
      outline: { state: 'ok' },
      distill: { state: 'ok' },
      cluster: { state: 'ok' },
    },
    ...patch,
  };
}

describe('source state', () => {
  it('calls a finished source complete', () => {
    expect(statusLabel(source())).toBe('complete');
    expect(progressOf(source())).toBeNull();
  });

  it('reports a partial derivation with parts and open claims', () => {
    const value = source({ parts_done: 4, atoms_left: 2 });
    expect(statusLabel(value)).toBe('partial · 4/10 parts · 2 open');
    expect(progressOf(value)).toMatchObject({ parts: 40, claims: 60 });
  });

  it('lets live counters win over the API snapshot', () => {
    const value = source({ atom_count: 1, parts_done: 1, atoms_left: 0 });
    const live = { phase: 'distill', fields: { source_atoms: 4, atoms_left: 1, parts_done: 3 } };
    expect(countsOf(value, live)).toMatchObject({ claims: 4, open: 1, partsDone: 3 });
  });

  it('shows a queued run with the number of runs ahead', () => {
    const value = source({ running_step: 'distill' });
    expect(statusLabel(value, { phase: 'queued', fields: { queued_ahead: 3 } })).toBe('queued · 3 ahead');
  });

  it('marks a bookmark without a derivation', () => {
    expect(statusLabel(source({ bookmark: true }))).toBe('bookmark');
    expect(progressOf(source({ bookmark: true }))).toBeNull();
  });

  it('reports a walled page as blocked, not as a dead link', () => {
    const walled = source({
      kind: 'url',
      atom_count: 0,
      provenance_rows: 0,
      parts_total: 0,
      parts_done: 0,
      steps: {
        capture: { state: 'failed', detail: 'login required', error: 'the page answered with a login wall' },
        outline: { state: 'skipped' },
        distill: { state: 'skipped' },
        cluster: { state: 'ok' },
      },
    });
    expect(statusLabel(walled)).toBe('blocked · login required');
  });

  it('still reports a dead link as a failure', () => {
    const dead = source({
      kind: 'url',
      atom_count: 0,
      provenance_rows: 0,
      parts_total: 0,
      parts_done: 0,
      steps: {
        capture: { state: 'failed', error: 'dial tcp: no such host' },
        outline: { state: 'skipped' },
        distill: { state: 'skipped' },
        cluster: { state: 'ok' },
      },
    });
    expect(statusLabel(dead)).toBe('link failed · dial tcp: no such host');
  });

  it('names a feed and a sitemap as lists, not as documents stuck after step 1', () => {
    const feed = source({
      kind: 'url',
      rss: true,
      atom_count: 0,
      provenance_rows: 0,
      parts_total: 0,
      parts_done: 0,
      tree_state: 'none',
      steps: {
        capture: { state: 'ok' },
        outline: { state: 'skipped' },
        distill: { state: 'skipped' },
        cluster: { state: 'skipped' },
      },
    });
    expect(statusLabel(feed)).toBe('feed · the items are sources');
    expect(progressOf(feed)).toBeNull();
    expect(totalsLabel(feed)).toBe('');

    const sitemap = source({
      kind: 'url',
      sitemap: true,
      atom_count: 0,
      provenance_rows: 0,
      parts_total: 0,
      parts_done: 0,
      tree_state: 'none',
      steps: {
        capture: { state: 'ok' },
        outline: { state: 'skipped' },
        distill: { state: 'skipped' },
        cluster: { state: 'skipped' },
      },
    });
    expect(statusLabel(sitemap)).toBe('sitemap · the pages are sources');
    expect(progressOf(sitemap)).toBeNull();
    expect(totalsLabel(sitemap)).toBe('');
  });

  it('offers a snapshot for a blocked URL, a bookmark and an attached snapshot', () => {
    expect(canAttachSnapshot(source({ kind: 'url', steps: { capture: { state: 'failed' } } }))).toBe(true);
    expect(canAttachSnapshot(source({ kind: 'url', bookmark: true }))).toBe(true);
    expect(canAttachSnapshot(source({ kind: 'url', attached: true }))).toBe(true);
  });

  it('does not offer a snapshot for a readable page, a file, a sitemap or a feed', () => {
    expect(canAttachSnapshot(source({ kind: 'url' }))).toBe(false);
    expect(canAttachSnapshot(source({ kind: 'md' }))).toBe(false);
    expect(canAttachSnapshot(source({ kind: 'pdf' }))).toBe(false);
    expect(canAttachSnapshot(source({ kind: 'url', sitemap: true }))).toBe(false);
    expect(canAttachSnapshot(source({ kind: 'url', rss: true }))).toBe(false);
  });

  it('filters sources by the attachment marker alone and together with the other filters', () => {
    const attached = source({
      kind: 'url',
      attached: true,
      title: 'Member article',
      tags: ['member'],
      steps: { capture: { state: 'ok' }, outline: { state: 'ok' }, distill: { state: 'ok' } },
    });
    const plain = source({ kind: 'url', title: 'Open article' });
    const markdown = source({ kind: 'md', title: 'Notes' });

    expect(matchesSourceFilter(attached, { attachedOnly: true })).toBe(true);
    expect(matchesSourceFilter(plain, { attachedOnly: true })).toBe(false);
    expect(matchesSourceFilter(attached, { attachedOnly: false })).toBe(true);
    expect(matchesSourceFilter(markdown, { attachedOnly: true })).toBe(false);

    // The attachment switch composes with kind, tag and search, like every other lens filter.
    expect(matchesSourceFilter(attached, { attachedOnly: true, kind: 'url' })).toBe(true);
    expect(matchesSourceFilter(attached, { attachedOnly: true, kind: 'md' })).toBe(false);
    expect(matchesSourceFilter(attached, { attachedOnly: true, tag: 'member' })).toBe(true);
    expect(matchesSourceFilter(attached, { attachedOnly: true, tag: 'other' })).toBe(false);
    expect(matchesSourceFilter(attached, { attachedOnly: true, query: 'member' })).toBe(true);
    expect(matchesSourceFilter(attached, { attachedOnly: true, query: 'open' })).toBe(false);
  });
});

describe('pipelineLabel', () => {
  const base: Pipeline = { active: 'idle', running: {}, queued: {}, asking: 0 };

  it('names the document steps and their count', () => {
    expect(pipelineLabel({ ...base, active: 'documents', running: { distill: 2 } })).toBe('distill · 2 sources');
  });

  it('never reports documents and clusters together', () => {
    expect(pipelineLabel({ ...base, active: 'clusters', running: { cluster: 1 } })).toBe('clustering · 1 pass');
  });

  it('reports a waiting cluster pass', () => {
    expect(pipelineLabel({ ...base, queued: { cluster: 1 } })).toBe('cluster pass waiting for the documents');
  });

  it('reports queries that run beside everything else', () => {
    expect(pipelineLabel({ ...base, asking: 1 })).toBe('asking…');
  });
});

describe('overall lamp', () => {
  it('is full and green when every step is done', () => {
    const done = source();
    expect(overallState(done)).toBe('ok');
    expect(overallPercent(done)).toBe(100);
  });

  it('lets a failure outrank the done steps', () => {
    const failed = source({
      steps: {
        capture: { state: 'ok' },
        outline: { state: 'ok' },
        distill: { state: 'failed', error: 'endpoint timeout' },
        cluster: { state: 'pending' },
      },
    });
    expect(overallState(failed)).toBe('failed');
  });

  it('reports a run, an orphaned index row and a stale input', () => {
    expect(overallState(source({ running_step: 'distill' }))).toBe('running');
    expect(overallState(source({ atom_count: 0, provenance_rows: 2 }))).toBe('stale');
    expect(
      overallState(
        source({
          steps: {
            capture: { state: 'ok' },
            outline: { state: 'ok' },
            distill: { state: 'stale' },
            cluster: { state: 'pending' },
          },
        }),
      ),
    ).toBe('stale');
  });

  it('shows the slower of parts and claims while the source is partial', () => {
    const partial = source({
      parts_total: 10,
      parts_done: 4,
      atom_count: 5,
      atoms_left: 2,
      steps: {
        capture: { state: 'ok' },
        outline: { state: 'ok' },
        distill: { state: 'pending' },
        cluster: { state: 'pending' },
      },
    });
    expect(overallState(partial)).toBe('pending');
    expect(overallPercent(partial)).toBe(40);
  });

  it('treats a bookmark as skipped and a feed as a complete list', () => {
    expect(overallState(source({ bookmark: true }))).toBe('skipped');
    expect(overallPercent(source({ bookmark: true }))).toBe(0);

    const feed = source({
      rss: true,
      atom_count: 0,
      provenance_rows: 0,
      parts_total: 0,
      parts_done: 0,
      steps: {
        capture: { state: 'ok' },
        outline: { state: 'skipped' },
        distill: { state: 'skipped' },
        cluster: { state: 'skipped' },
      },
    });
    expect(overallState(feed)).toBe('ok');
    expect(overallPercent(feed)).toBe(100);
  });
});
