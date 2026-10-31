import type { Pipeline, Source, StepName, StepState, StepStatus } from './api/types';
import { t, tn } from './i18n/index.svelte';

/** The four steps, in order. The first three are document steps (one source); cluster runs over the
 *  whole vault and only while no document step runs. */
export const PIPELINE_STEPS: readonly StepName[] = ['capture', 'outline', 'distill', 'cluster'];

/** STEP_LABEL maps a step to the catalog key that names it. The sentence is resolved with t() where
 *  it is shown, so a rendered row follows the interface language. */
export const STEP_LABEL: Record<StepName, string> = {
  capture: 'sources.step.capture',
  outline: 'sources.step.outline',
  distill: 'sources.step.distill',
  cluster: 'sources.step.cluster',
};

/** WALL_LABELS are the capture details a blocked URL carries (entities.WallKind.Label). They are
 *  what tells a wall apart from a dead link: the page answered, just not with itself. The server's
 *  detail stays the value that is matched; only the sentence the reader sees is translated. */
const WALL_KEYS: Record<string, string> = {
  'login required': 'sources.wall.loginRequired',
  paywalled: 'sources.wall.paywalled',
  'consent required': 'sources.wall.consentRequired',
};
const WALL_LABELS = new Set(Object.keys(WALL_KEYS));

export interface LiveProgress {
  phase?: string;
  msg?: string;
  fields: Record<string, number>;
}

export interface SourceCounts {
  partsDone: number;
  partsTotal: number;
  claims: number;
  open: number;
  phase: string;
  msg: string;
  queuedAhead: number;
}

export interface SourceProgress {
  parts: number;
  claims: number;
  partsDone: number;
  partsTotal: number;
  claimsDone: number;
  claimsTotal: number;
}

/** countsOf merges the last API snapshot with the live counters of an in-flight run.
 *
 *  While a source is running, live values win: a list row and the source page must not disagree
 *  about how many claims exist right now. The backlog is clamped so mixed snapshots cannot make a
 *  bar jump to 100% and back.
 */
export function countsOf(source: Source, live?: LiveProgress): SourceCounts {
  const fields = live?.fields ?? {};
  const num = (key: string, fallback: number): number =>
    typeof fields[key] === 'number' && Number.isFinite(fields[key]) ? fields[key] : fallback;

  const baseClaims = source.atom_count ?? 0;
  const claims = num('source_atoms', baseClaims);
  const hasLiveBacklog = typeof fields.atoms_left === 'number';
  let open = hasLiveBacklog ? fields.atoms_left : (source.atoms_left ?? 0);
  // Only the stale snapshot needs the adjustment: claims added since the snapshot are necessarily
  // unassociated, but a live backlog already accounts for them.
  if (!hasLiveBacklog && claims > baseClaims) {
    open = Math.max(open, (source.atoms_left ?? 0) + (claims - baseClaims));
  }
  open = Math.min(Math.max(0, open), claims);

  return {
    partsTotal: num('parts_total', source.parts_total ?? 0),
    partsDone: num('parts_done', source.parts_done ?? 0),
    claims,
    open,
    phase: live?.phase ?? '',
    msg: live?.msg ?? '',
    queuedAhead: num('queued_ahead', 0),
  };
}

export function stepsOf(source: Source): Record<StepName, StepStatus> {
  const out = {} as Record<StepName, StepStatus>;
  for (const step of PIPELINE_STEPS) out[step] = source.steps?.[step] ?? { state: 'pending' };
  return out;
}

/** overallState collapses the four step lights into ONE state for a narrow list row.
 *
 *  A failure outranks a run, a stale input outranks "done", and a bookmark is deliberately skipped
 *  (it has no derivation). A feed or a sitemap is a complete LIST: capture is ok, the other steps do
 *  not apply — that is done, not pending.
 */
export function overallState(source: Source, live?: LiveProgress): StepState {
  if (source.bookmark) return 'skipped';
  if (source.running_step) return 'running';
  if (source.atom_count === 0 && source.provenance_rows > 0) return 'stale';
  const steps = stepsOf(source);
  const states = PIPELINE_STEPS.map((step) => steps[step].state);
  if (states.includes('failed') || source.tree_state === 'failed') return 'failed';
  if (states.includes('stale')) return 'stale';
  if (states.includes('running')) return 'running';
  if (states.every((state) => state === 'ok' || state === 'skipped')) return 'ok';
  return 'pending';
}

/** overallPercent is the one number the narrow lamp's ring shows: the slower of the two dimensions
 *  (parts distilled, claims associated). A source with nothing left is full; a failed or pending
 *  source keeps whatever it already reached. */
export function overallPercent(source: Source, live?: LiveProgress): number {
  if (source.bookmark) return 0;
  const progress = progressOf(source, live);
  if (progress) return Math.min(progress.parts, progress.claims);
  return overallState(source, live) === 'ok' ? 100 : 0;
}

/** canAttachSnapshot reports whether a human-supplied page can fill this source: a URL the app could
 *  not read — a wall (capture failed), a bookmark (no readable text: a JavaScript page, a video, an
 *  app), or a source whose snapshot was already attached (the human may replace it). A sitemap or a
 *  feed is a LIST, not a page: attaching to it would replace the list. */
export function canAttachSnapshot(source: Source): boolean {
  if (source.kind !== 'url' || source.sitemap === true || source.rss === true) return false;
  if (source.bookmark === true || source.attached === true) return true;
  return source.steps?.capture?.state === 'failed';
}

/** canRefreshSource reports whether a manual Refresh is offered for this URL source. A normal page
 *  is refreshed by the background cadence; only a source the app could not read (a wall, including
 *  one whose readable snapshot survived it) and a feed copy need the explicit action. */
export function canRefreshSource(source: Source): boolean {
  if (source.kind !== 'url' || source.sitemap === true || source.rss === true || source.attached === true) {
    return false;
  }
  return source.from_feed === true || !!source.wall;
}

/** SourceFilter is the source lens's client-side filter: the search term, the kind selector, one
 *  tag and the "attached snapshots only" switch. The list is loaded whole, so the same predicate
 *  serves every page. */
export interface SourceFilter {
  query?: string;
  kind?: string;
  tag?: string;
  attachedOnly?: boolean;
}

/** matchesSourceFilter applies the lens filters. An attachment is a source fact (a human supplied
 *  the snapshot), not a derivation state: the filter shows those sources regardless of their step
 *  lights. */
export function matchesSourceFilter(source: Source, filter: SourceFilter): boolean {
  if (filter.kind && filter.kind !== 'all' && source.kind !== filter.kind) return false;
  if (filter.attachedOnly && source.attached !== true) return false;
  if (filter.tag) {
    const tags = new Set([...(source.tags ?? []), ...(source.auto_tags ?? [])]);
    if (!tags.has(filter.tag)) return false;
  }
  const term = (filter.query ?? '').trim().toLowerCase();
  if (!term) return true;
  return (
    source.title.toLowerCase().includes(term) ||
    source.original_name.toLowerCase().includes(term) ||
    (source.url ?? '').toLowerCase().includes(term) ||
    (source.tags ?? []).some((tag) => tag.includes(term)) ||
    (source.auto_tags ?? []).some((tag) => tag.includes(term))
  );
}

export function stepTitle(step: StepName, status: StepStatus): string {
  const bits = [t(STEP_LABEL[step])];
  if (status.detail) bits.push(status.detail);
  if (status.error) bits.push(t('sources.stepTitle.failed', { error: status.error }));
  if (status.state === 'pending') bits.push(t('sources.stepTitle.pending'));
  if (status.state === 'skipped') bits.push(t('sources.stepTitle.skipped'));
  if (status.state === 'failed') bits.push(t('sources.stepTitle.blocking'));
  if (status.state === 'running') bits.push(t('sources.stepTitle.running'));
  if (status.state === 'stale') bits.push(t('sources.stepTitle.stale'));
  return bits.join(' · ');
}

/** stepStateLabel names one step's state; the step lights read it in their aria-label. */
export function stepStateLabel(state: StepState): string {
  return t(`sources.state.${state}`);
}

/** statusLabel names the derivation state in one calm line. */
export function statusLabel(source: Source, live?: LiveProgress): string {
  if (source.bookmark) return t('sources.status.bookmark');

  const running = source.running_step;
  if (running) {
    const counts = countsOf(source, live);
    if (counts.phase === 'queued') {
      return counts.queuedAhead > 0
        ? t('sources.status.queuedAhead', { count: counts.queuedAhead })
        : t('sources.status.queued');
    }
    return `${running}…`;
  }

  if (source.atom_count === 0) {
    if (source.provenance_rows > 0) return tn('sources.status.orphaned', source.provenance_rows);
    const capture = source.steps?.capture;
    if (capture?.state === 'failed') {
      // A wall is a different report from a dead link: the page answered, just not with itself.
      const detail = (capture.detail || '').trim().split('\n')[0];
      if (WALL_LABELS.has(detail)) {
        return t('sources.status.blocked', { reason: t(WALL_KEYS[detail]) });
      }
      const why = (capture.error || capture.detail || '').split('\n')[0].trim();
      const short = why.length > 64 ? `${why.slice(0, 64)}…` : why;
      return short
        ? t('sources.status.linkFailed', { error: short })
        : t('sources.status.linkFailedShort');
    }
    if (capture?.state === 'pending' && !source.path) return t('sources.status.fetching');
    // A feed or a sitemap is a LIST of sources: capture fetched the list, and there is no document
    // to outline, distil or associate. Naming it is what separates a finished list from a source
    // stuck after step 1.
    if (source.rss) return t('sources.status.feedList');
    if (source.sitemap) return t('sources.status.sitemapList');
    if (source.steps?.distill?.state === 'ok') return t('sources.status.completeNoClaims');
    if (source.tree_state === 'built') return t('sources.status.ready');
    if (source.tree_state === 'failed') return t('sources.status.outlineFailed');
    return source.tree_state || t('sources.status.waiting');
  }

  const counts = countsOf(source, live);
  const partsOpen = counts.partsTotal > 0 && counts.partsDone < counts.partsTotal;
  if (partsOpen || counts.open > 0) {
    const bits: string[] = [];
    if (partsOpen) {
      bits.push(t('sources.status.partsOpen', { done: counts.partsDone, total: counts.partsTotal }));
    }
    if (counts.open > 0) bits.push(t('sources.status.claimsOpen', { count: counts.open }));
    return t('sources.status.partial', { parts: bits.join(' · ') });
  }
  return t('sources.status.complete');
}

/** progressOf returns the two dimensions of derivation, or null when nothing is left to show.
 *
 *  parts: how much of the document has been distilled.
 *  claims: how much of what was distilled is associated.
 *  A dimension with nothing to do counts as done, so a finished source never shows an empty bar.
 */
export function progressOf(source: Source, live?: LiveProgress): SourceProgress | null {
  if (source.bookmark || source.rss || source.sitemap) return null;
  const counts = countsOf(source, live);
  const partsComplete = counts.partsTotal === 0 || counts.partsDone >= counts.partsTotal;
  const partsPct = counts.partsTotal > 0 ? counts.partsDone / counts.partsTotal : 1;
  const claimsPct = counts.claims > 0 ? (counts.claims - counts.open) / counts.claims : partsComplete ? 1 : 0;
  if (partsPct >= 1 && claimsPct >= 1) return null;
  return {
    parts: Math.round(partsPct * 100),
    claims: Math.round(claimsPct * 100),
    partsDone: counts.partsDone,
    partsTotal: counts.partsTotal,
    claimsDone: counts.claims - counts.open,
    claimsTotal: counts.claims,
  };
}

export function totalsLabel(source: Source, live?: LiveProgress): string {
  // A feed or a sitemap has no parts and no claims of its own: "0 parts · 0 claims" reads like a
  // document that never started. Its count lives in the Sub-sources tab.
  if (source.rss || source.sitemap) return '';
  const counts = countsOf(source, live);
  return `${tn('sources.counts.parts', counts.partsTotal)} · ${tn('sources.counts.claims', counts.claims)}`;
}

/** pipelineLabel names what the application is doing right now, globally. */
export function pipelineLabel(pipeline: Pipeline | null): string {
  if (!pipeline) return '';
  const running = pipeline.running ?? {};
  const queued = pipeline.queued ?? {};
  const sum = (map: Record<string, number>, ...steps: StepName[]): number =>
    steps.reduce((total, step) => total + (map[step] ?? 0), 0);

  if ((running.cluster ?? 0) > 0) {
    const count = running.cluster ?? 0;
    return tn('sources.pipeline.clustering', count);
  }

  const documents = sum(running, 'capture', 'outline', 'distill');
  if (documents > 0) {
    const active = PIPELINE_STEPS.filter((step) => step !== 'cluster' && (running[step] ?? 0) > 0);
    return tn('sources.pipeline.documents', documents, { steps: active.join(' + ') });
  }
  if ((queued.cluster ?? 0) > 0) return t('sources.pipeline.clusterWaiting');
  const documentQueue = sum(queued, 'capture', 'outline', 'distill');
  if (documentQueue > 0) return t('sources.pipeline.queued', { count: documentQueue });
  if ((pipeline.asking ?? 0) > 0) return t('sources.pipeline.asking');
  return '';
}

/** stepOf names a maintenance job the way the Status page speaks about it. */
export function jobStepLabel(step: string): string {
  switch (step) {
    case 'capture':
      return t('sources.job.capture');
    case 'outline':
      return t('sources.job.outline');
    case 'distill':
      return t('sources.job.distill');
    case 'cluster':
      return t('sources.job.cluster');
    case 'query':
      return t('sources.job.query');
    case 'lint':
      return t('sources.job.lint');
    case 'vocabulary':
      return t('sources.job.vocabulary');
    case 'reindex':
      return t('sources.job.reindex');
    case 'reconcile':
      return t('sources.job.reconcile');
    case 'cleanup':
      return t('sources.job.cleanup');
    case 'rebuild-trees':
      return t('sources.job.rebuildTrees');
    case 'refresh-urls':
      return t('sources.job.refreshUrls');
    case 'refresh-source':
      return t('sources.job.refreshSource');
    case 'fix-urls':
      return t('sources.job.fixUrls');
    default:
      return step;
  }
}
