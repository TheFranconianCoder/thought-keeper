/**
 * The one rule every chip cloud follows: show the head, keep the tail one click away.
 *
 * A cloud is a FACET, not a ranking. The most-used names are the fastest way in, but the rare ones
 * are often the precise ones — so the tail is never deleted, only folded. The cut is by RANK, not by
 * a share of the strongest count: one dominant tag must not decide which middle names disappear.
 */
export const CLOUD_LIMIT = 18;
/** Below this many entries nothing is hidden at all; a small cloud is already calm. */
export const CLOUD_THRESHOLD = 24;

export interface CloudOptions<T> {
  expanded: boolean;
  /** The key of the active filter; it is always visible, even when it falls outside the head. */
  active?: string;
  key: (item: T) => string;
  count: (item: T) => number;
  limit?: number;
  threshold?: number;
}

export interface CloudSelection<T> {
  visible: T[];
  /** How many entries the folded tail holds. Zero when everything is showing. */
  hidden: number;
  /** Whether the cloud had more entries than the threshold, i.e. whether "show less" applies. */
  truncated: boolean;
}

/** visibleCloud folds a cloud's long tail, keeping the head, the active filter and an honest count. */
export function visibleCloud<T>(items: readonly T[], options: CloudOptions<T>): CloudSelection<T> {
  const {
    expanded,
    active = '',
    key,
    count,
    limit = CLOUD_LIMIT,
    threshold = CLOUD_THRESHOLD,
  } = options;
  const truncated = items.length > threshold;
  if (!truncated || expanded) {
    return { visible: [...items], hidden: 0, truncated };
  }
  const visible = [...items]
    .sort((a, b) => count(b) - count(a) || key(a).localeCompare(key(b)))
    .slice(0, Math.max(1, limit));
  if (active && !visible.some((item) => key(item) === active)) {
    // The reader's current filter must not vanish just because it is rare. It keeps its highlight
    // and joins the visible set at the end; the hidden count stays exact.
    const current = items.find((item) => key(item) === active);
    if (current) visible.push(current);
  }
  return { visible, hidden: items.length - visible.length, truncated };
}
