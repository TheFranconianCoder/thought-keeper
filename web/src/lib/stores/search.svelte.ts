import { api } from '../api';
import { humanError } from '../api/errors';
import type { SearchHit } from '../api/types';

export type SearchLayer = 'atoms' | 'sources';

class SearchStore {
  query = $state('');
  layer = $state<SearchLayer>('atoms');
  hits = $state<SearchHit[]>([]);
  active = $state(0);
  busy = $state(false);
  error = $state('');

  private timer: ReturnType<typeof setTimeout> | undefined;
  private controller: AbortController | null = null;
  private seq = 0;

  setQuery(value: string): void {
    this.query = value;
    this.active = 0;
    clearTimeout(this.timer);
    const term = value.trim();
    if (term.length < 2) {
      this.cancel();
      this.hits = [];
      this.busy = false;
      this.error = '';
      return;
    }
    this.timer = setTimeout(() => void this.run(term), 170);
  }

  setLayer(layer: SearchLayer): void {
    if (this.layer === layer) return;
    this.layer = layer;
    this.active = 0;
    const term = this.query.trim();
    if (term.length >= 2) {
      clearTimeout(this.timer);
      this.timer = setTimeout(() => void this.run(term), 30);
    }
  }

  private cancel(): void {
    this.controller?.abort();
    this.controller = null;
  }

  private async run(term: string): Promise<void> {
    this.cancel();
    const seq = ++this.seq;
    const controller = new AbortController();
    this.controller = controller;
    this.busy = true;
    this.error = '';
    try {
      const result = await api.search(term, this.layer, 30, controller.signal);
      if (seq !== this.seq) return;
      this.hits = result.hits ?? [];
      this.active = 0;
    } catch (error) {
      if (seq !== this.seq) return;
      this.hits = [];
      this.error = humanError(error);
    } finally {
      if (seq === this.seq) this.busy = false;
    }
  }

  move(delta: number): void {
    if (this.hits.length === 0) return;
    const next = Math.min(this.hits.length - 1, Math.max(0, this.active + delta));
    this.active = next;
  }

  reset(): void {
    clearTimeout(this.timer);
    this.cancel();
    this.seq++;
    this.query = '';
    this.hits = [];
    this.active = 0;
    this.busy = false;
    this.error = '';
  }
}

export const search = new SearchStore();
