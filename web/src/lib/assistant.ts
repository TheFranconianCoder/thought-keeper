import type { CommandCriteria } from './api/types';
import { t } from './i18n/index.svelte';

/** Chip is one piece of the reading: what the request was actually understood as. */
export interface Chip {
  key: string;
  text: string;
}

/**
 * criteriaChips turns the interpretation into the words a reader checks it against. Without it the
 * banner would only repeat the utterance, and a good reading could not be told from a bad one.
 *
 * `from_topic` is deliberately worded differently from `topic`: it does not filter a field, it asks
 * which sources own claims about that subject. Same word, different promise. The window's verb
 * follows the lens: sources are added, claims are created, wiki pages are edited.
 */
export function criteriaChips(c: CommandCriteria, lens = 'sources'): Chip[] {
  const chips: Chip[] = [];
  if (c.from_topic) {
    chips.push({ key: 'from_topic', text: t('assistant.criteria.from_topic', { topic: c.from_topic }) });
  }
  if (c.source) {
    chips.push({ key: 'source', text: t('assistant.criteria.source', { ref: shortRef(c.source) }) });
  }
  if (c.atom) {
    chips.push({ key: 'atom', text: t('assistant.criteria.atom', { ref: shortRef(c.atom) }) });
  }
  if (c.tag) chips.push({ key: 'tag', text: t('assistant.criteria.tag', { tag: c.tag }) });
  for (const tag of c.tags ?? []) {
    chips.push({ key: `tag:${tag}`, text: t('assistant.criteria.tag', { tag }) });
  }
  for (const topic of c.topics ?? []) {
    chips.push({ key: `topic:${topic}`, text: t('assistant.criteria.topic', { topic }) });
  }
  if (c.topic) chips.push({ key: 'topic', text: t('assistant.criteria.topic', { topic: c.topic }) });
  if (c.kind) chips.push({ key: 'kind', text: t('assistant.criteria.kind', { kind: c.kind }) });
  if (c.window?.spec) {
    chips.push({ key: 'window', text: windowChip(c.window.spec, lens) });
  }
  if (c.text) chips.push({ key: 'text', text: t('assistant.criteria.text', { text: c.text }) });
  if (c.state) chips.push({ key: 'state', text: t(`assistant.state.${c.state}`) });
  if (c.bookmarks_only) chips.push({ key: 'bookmarks', text: t('assistant.criteria.bookmarks') });
  if (c.attached_only) chips.push({ key: 'attached', text: t('assistant.criteria.attached') });
  if (c.order && c.order !== 'newest' && c.order !== 'relevance') {
    chips.push({ key: 'order', text: t('assistant.criteria.order', { order: t(`assistant.order.${c.order}`) }) });
  }
  return chips;
}

/** windowChip names the verb a lens uses for its clock. */
export function windowChip(spec: string, lens = 'sources'): string {
  const key =
    lens === 'claims'
      ? 'assistant.criteria.window.claims'
      : lens === 'wiki'
        ? 'assistant.criteria.window.wiki'
        : 'assistant.criteria.window.sources';
  return t(key, { window: windowLabel(spec) });
}

/** shortRef renders an id without its middle: a chip is a label, not a pointer. */
function shortRef(ref: string): string {
  return ref.length > 12 ? `${ref.slice(0, 6)}…${ref.slice(-4)}` : ref;
}

/** windowLabel reads a window spec as the phrase a reader typed. */
export function windowLabel(spec: string): string {
  const value = spec.trim().toLowerCase();
  const known: Record<string, string> = {
    today: 'assistant.window.today',
    yesterday: 'assistant.window.yesterday',
    'this-week': 'assistant.window.this_week',
    'last-week': 'assistant.window.last_week',
    'this-month': 'assistant.window.this_month',
    'last-month': 'assistant.window.last_month',
    'since-yesterday': 'assistant.window.since_yesterday',
  };
  if (known[value]) return t(known[value]);

  const rolling = /^last-(\d+)([hdwm])$/.exec(value);
  if (rolling) {
    const count = Number(rolling[1]);
    switch (rolling[2]) {
      case 'h':
        return t('assistant.window.hours', { count });
      case 'd':
        return t('assistant.window.days', { count });
      case 'w':
        return t('assistant.window.weeks', { count });
      case 'm':
        return t('assistant.window.months', { count });
    }
  }

  if (value.includes('..')) {
    const [from, to] = value.split('..', 2);
    return `${formatDay(from)} – ${formatDay(to)}`;
  }
  if (/^\d{4}-\d{2}-\d{2}$/.test(value)) return formatDay(value);
  return spec;
}

/** formatDay turns an ISO day into the reader's local date, without a timezone shift. */
function formatDay(iso: string): string {
  const parts = iso.split('-').map(Number);
  if (parts.length !== 3 || parts.some((n) => !Number.isFinite(n))) return iso;
  const [year, month, day] = parts;
  return new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric' }).format(
    new Date(year, month - 1, day),
  );
}
