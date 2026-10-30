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
 * which sources own claims about that subject. Same word, different promise.
 */
export function criteriaChips(c: CommandCriteria): Chip[] {
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
  if (c.text) chips.push({ key: 'text', text: t('assistant.criteria.text', { text: c.text }) });
  if (c.since) {
    chips.push({ key: 'since', text: t('assistant.criteria.since', { window: windowLabel(c.since) }) });
  }
  if (c.state) chips.push({ key: 'state', text: t(`assistant.state.${c.state}`) });
  if (c.bookmarks_only) chips.push({ key: 'bookmarks', text: t('assistant.criteria.bookmarks') });
  if (c.attached_only) chips.push({ key: 'attached', text: t('assistant.criteria.attached') });
  if (c.order && c.order !== 'newest' && c.order !== 'relevance') {
    chips.push({ key: 'order', text: t('assistant.criteria.order', { order: t(`assistant.order.${c.order}`) }) });
  }
  return chips;
}

/** shortRef renders an id without its middle: a chip is a label, not a pointer. */
function shortRef(ref: string): string {
  return ref.length > 12 ? `${ref.slice(0, 6)}…${ref.slice(-4)}` : ref;
}

/** windowLabel reads the server's duration ("168h") as a sentence. */
export function windowLabel(since: string): string {
  const match = /^(\d+)h$/.exec(since.trim());
  if (!match) return since;
  const hours = Number(match[1]);
  if (hours % 24 === 0) return t('assistant.window.days', { count: hours / 24 });
  return t('assistant.window.hours', { count: hours });
}
