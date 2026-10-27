import { api } from '../api';
import { humanError } from '../api/errors';
import type { Atom, Branch, TopicUse } from '../api/types';

export const CLAIM_PAGE_SIZE = 50;
export const TOPIC_CLOUD_SIZE = 32;

class ClaimsStore {
  items = $state<Atom[]>([]);
  total = $state(0);
  topic = $state('');
  topics = $state<Branch[]>([]);
  topicCounts = $state<Record<string, number>>({});
  loaded = $state(false);
  error = $state('');
  /** A state event arrived while the Claims view was hidden: the list must reload on entry. */
  stale = $state(false);
  /** The same for the topic cloud, which the Ask view also reads. */
  topicsStale = $state(false);

  selectedId = $state('');
  selected = $state<Atom | null>(null);
  /** The claims this claim travels with in a cluster. */
  relatedPeers = $state<Atom[]>([]);
  /** The claims that only share its flat topic — never a cluster peer. */
  relatedSameTopic = $state<Atom[]>([]);
  detailLoading = $state(false);
  detailError = $state('');

  private listSeq = 0;
  private topicSeq = 0;
  private detailSeq = 0;
  /** Bumped by a stale mark, so a load that started earlier cannot clear a newer mark. */
  private staleSeq = 0;
  private topicsStaleSeq = 0;

  async load(topic = this.topic): Promise<void> {
    this.topic = topic;
    const seq = ++this.listSeq;
    const staleSeq = this.staleSeq;
    try {
      const result = await api.atoms(topic, CLAIM_PAGE_SIZE);
      if (seq !== this.listSeq) return;
      this.items = result.atoms ?? [];
      this.total = result.count ?? this.items.length;
      this.loaded = true;
      this.error = '';
      if (staleSeq === this.staleSeq) this.stale = false;
    } catch (error) {
      if (seq !== this.listSeq) return;
      this.items = [];
      this.total = 0;
      this.error = humanError(error);
    }
  }

  async loadTopics(): Promise<void> {
    const seq = ++this.topicSeq;
    const staleSeq = this.topicsStaleSeq;
    try {
      const result = await api.claimTopics();
      if (seq !== this.topicSeq) return;
      const all = (result.topics ?? []).filter((topic: TopicUse) => topic.topic);
      this.topicCounts = Object.fromEntries(all.map((topic) => [topic.topic, topic.count]));
      const next = all
        .slice()
        .sort((a, b) => b.count - a.count || a.topic.localeCompare(b.topic))
        .slice(0, TOPIC_CLOUD_SIZE)
        .map((topic) => ({ topic: topic.topic, count: topic.count }));
      if (JSON.stringify(next) !== JSON.stringify(this.topics)) this.topics = next;
      if (staleSeq === this.topicsStaleSeq) this.topicsStale = false;
    } catch {
      if (seq === this.topicSeq && this.topics.length) {
        this.topics = [];
        this.topicCounts = {};
      }
    }
  }

  async open(id: string): Promise<void> {
    this.selectedId = id;
    if (this.selected?.id !== id) {
      this.selected = null;
      this.relatedPeers = [];
      this.relatedSameTopic = [];
      this.detailError = '';
    }
    const seq = ++this.detailSeq;
    this.detailLoading = true;
    try {
      const [atom, related] = await Promise.all([
        api.atom(id),
        api.atomRelated(id).catch(() => ({ peers: [], same_topic: [], count: 0 })),
      ]);
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.selected = atom;
      this.relatedPeers = related.peers ?? [];
      this.relatedSameTopic = related.same_topic ?? [];
      this.detailError = '';
    } catch (error) {
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.detailError = humanError(error);
    } finally {
      if (seq === this.detailSeq) this.detailLoading = false;
    }
  }

  clearSelection(): void {
    this.detailSeq++;
    this.selectedId = '';
    this.selected = null;
    this.relatedPeers = [];
    this.relatedSameTopic = [];
    this.detailError = '';
    this.detailLoading = false;
  }

  /** markStale records a claim event that arrived while the Claims view was hidden. */
  markStale(): void {
    this.staleSeq++;
    this.stale = true;
  }

  /** markTopicsStale is the same for the topic cloud, which the Ask view also reads. */
  markTopicsStale(): void {
    this.topicsStaleSeq++;
    this.topicsStale = true;
  }

  /** refresh reloads only what is currently visible: the list, the topic cloud and an open claim. */
  async refresh(): Promise<void> {
    await Promise.all([this.load(this.topic), this.loadTopics()]);
    if (this.selectedId) await this.open(this.selectedId);
  }

  setTotalFallback(total: number): void {
    if (!this.loaded) this.total = total;
  }
}

export const claims = new ClaimsStore();
