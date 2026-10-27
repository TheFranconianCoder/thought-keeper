import type { Pipeline, Source, StepName, StepStatus } from './api/types';

/** The four steps, in order. The first three are document steps (one source); cluster runs over the
 *  whole vault and only while no document step runs. */
export const PIPELINE_STEPS: readonly StepName[] = ['capture', 'outline', 'distill', 'cluster'];

export const STEP_LABEL: Record<StepName, string> = {
  capture: 'capture — read the source into a local snapshot',
  outline: 'outline — build the document structure',
  distill: 'distill — one claim per statement',
  cluster: 'cluster — group claims into titleless associations',
};

/** WALL_LABELS are the capture details a blocked URL carries (entities.WallKind.Label). They are
 *  what tells a wall apart from a dead link: the page answered, just not with itself. */
const WALL_LABELS = new Set(['login required', 'paywalled', 'consent required']);

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

/** canAttachSnapshot reports whether a human-supplied page can fill this source: a URL the app could
 *  not read — a wall (capture failed), a bookmark (no readable text: a JavaScript page, a video, an
 *  app), or a source whose snapshot was already attached (the human may replace it). A sitemap or a
 *  feed is a LIST, not a page: attaching to it would replace the list. */
export function canAttachSnapshot(source: Source): boolean {
  if (source.kind !== 'url' || source.sitemap === true || source.rss === true) return false;
  if (source.bookmark === true || source.attached === true) return true;
  return source.steps?.capture?.state === 'failed';
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
  const bits = [STEP_LABEL[step]];
  if (status.detail) bits.push(status.detail);
  if (status.error) bits.push(`failed: ${status.error}`);
  if (status.state === 'pending') bits.push('not done yet');
  if (status.state === 'skipped') bits.push('does not apply');
  if (status.state === 'failed') bits.push('this blocks the next step');
  if (status.state === 'running') bits.push('running now');
  if (status.state === 'stale') bits.push('the input changed; derivation is stale');
  return bits.join(' · ');
}

/** statusLabel names the derivation state in one calm line. */
export function statusLabel(source: Source, live?: LiveProgress): string {
  if (source.bookmark) return 'bookmark';

  const running = source.running_step;
  if (running) {
    const counts = countsOf(source, live);
    if (counts.phase === 'queued') {
      return counts.queuedAhead > 0 ? `queued · ${counts.queuedAhead} ahead` : 'queued';
    }
    return `${running}…`;
  }

  if (source.atom_count === 0) {
    if (source.provenance_rows > 0) return `${source.provenance_rows} orphaned row(s) · reindex`;
    const capture = source.steps?.capture;
    if (capture?.state === 'failed') {
      // A wall is a different report from a dead link: the page answered, just not with itself.
      const detail = (capture.detail || '').trim().split('\n')[0];
      if (WALL_LABELS.has(detail)) return `blocked · ${detail}`;
      const why = (capture.error || capture.detail || '').split('\n')[0].trim();
      const short = why.length > 64 ? `${why.slice(0, 64)}…` : why;
      return short ? `link failed · ${short}` : 'link failed';
    }
    if (capture?.state === 'pending' && !source.path) return 'fetching link…';
    // A feed or a sitemap is a LIST of sources: capture fetched the list, and there is no document
    // to outline, distil or associate. Naming it is what separates a finished list from a source
    // stuck after step 1.
    if (source.rss) return 'feed · the items are sources';
    if (source.sitemap) return 'sitemap · the pages are sources';
    if (source.steps?.distill?.state === 'ok') return 'complete · no claims';
    if (source.tree_state === 'built') return 'ready to distill';
    if (source.tree_state === 'failed') return 'outline failed';
    return source.tree_state || 'waiting';
  }

  const counts = countsOf(source, live);
  const partsOpen = counts.partsTotal > 0 && counts.partsDone < counts.partsTotal;
  if (partsOpen || counts.open > 0) {
    const bits: string[] = [];
    if (partsOpen) bits.push(`${counts.partsDone}/${counts.partsTotal} parts`);
    if (counts.open > 0) bits.push(`${counts.open} open`);
    return `partial · ${bits.join(' · ')}`;
  }
  return 'complete';
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
  return `${counts.partsTotal} ${counts.partsTotal === 1 ? 'part' : 'parts'} · ${counts.claims} ${
    counts.claims === 1 ? 'claim' : 'claims'
  }`;
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
    return `clustering · ${count} pass${count === 1 ? '' : 'es'}`;
  }

  const documents = sum(running, 'capture', 'outline', 'distill');
  if (documents > 0) {
    const active = PIPELINE_STEPS.filter((step) => step !== 'cluster' && (running[step] ?? 0) > 0);
    return `${active.join(' + ')} · ${documents} source${documents === 1 ? '' : 's'}`;
  }
  if ((queued.cluster ?? 0) > 0) return 'cluster pass waiting for the documents';
  const documentQueue = sum(queued, 'capture', 'outline', 'distill');
  if (documentQueue > 0) return `${documentQueue} queued`;
  if ((pipeline.asking ?? 0) > 0) return 'asking…';
  return '';
}

/** stepOf names a maintenance job the way the Status page speaks about it. */
export function jobStepLabel(step: string): string {
  switch (step) {
    case 'capture':
      return 'capture';
    case 'outline':
      return 'outline';
    case 'distill':
      return 'distill';
    case 'cluster':
      return 'cluster claims';
    case 'query':
      return 'ask';
    case 'lint':
      return 'lint';
    case 'vocabulary':
      return 'curate names';
    case 'reindex':
      return 'reindex';
    case 'reconcile':
      return 'reconcile';
    case 'cleanup':
      return 'cleanup';
    case 'rebuild-trees':
      return 'rebuild outlines';
    case 'refresh-urls':
      return 'refresh URLs';
    case 'refresh-source':
      return 'refresh source';
    case 'fix-urls':
      return 'fix URLs';
    default:
      return step;
  }
}
