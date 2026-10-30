import { api } from '../api';
import { humanError } from '../api/errors';
import type { WikiPage, WikiSummary, WikiTag } from '../api/types';

export class WikiStore {
  pages = $state<WikiSummary[]>([]);
  tags = $state<WikiTag[]>([]);
  count = $state(0);
  loaded = $state(false);
  error = $state('');

  selectedId = $state('');
  selected = $state<WikiPage | null>(null);
  detailLoading = $state(false);
  detailError = $state('');

  filter = $state('');

  private listSeq = 0;
  private detailSeq = 0;

  async load(): Promise<void> {
    const seq = ++this.listSeq;
    try {
      const result = await api.wiki();
      if (seq !== this.listSeq) return;
      this.pages = result.pages ?? [];
      this.tags = result.tags ?? [];
      this.count = result.count ?? this.pages.length;
      this.loaded = true;
      this.error = '';
    } catch (error) {
      if (seq !== this.listSeq) return;
      this.loaded = true;
      this.error = humanError(error);
    }
  }

  async open(id: string): Promise<void> {
    this.selectedId = id;
    if (this.selected?.id !== id) {
      this.selected = null;
      this.detailError = '';
    }
    const seq = ++this.detailSeq;
    this.detailLoading = true;
    try {
      const page = await api.wikiPage(id);
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.selected = page;
      this.detailError = '';
    } catch (error) {
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.selected = null;
      this.detailError = humanError(error);
    } finally {
      if (seq === this.detailSeq) this.detailLoading = false;
    }
  }

  async save(id: string, page: { title: string; tags: string[]; body: string }): Promise<WikiPage> {
    const saved = await api.saveWiki(id, page);
    this.selectedId = saved.id;
    this.selected = saved;
    this.detailError = '';
    // The page list is a background refresh. Waiting for a full scan of the wiki directory was
    // part of why a save felt slow; the saved page itself is already on disk.
    const summary: WikiSummary = {
      id: saved.id,
      title: saved.title,
      tags: saved.tags,
      auto_tags: saved.auto_tags,
      source_hash: saved.source_hash,
      updated_at: saved.updated_at,
    };
    const index = this.pages.findIndex((entry) => entry.id === saved.id);
    if (index >= 0) this.pages[index] = summary;
    else this.pages = [summary, ...this.pages];
    this.count = this.pages.length;
    void this.load();
    return saved;
  }

  async remove(id: string): Promise<void> {
    await api.deleteWiki(id);
    if (this.selectedId === id) {
      this.selectedId = '';
      this.selected = null;
    }
    await this.load();
  }

  clearSelection(): void {
    this.detailSeq++;
    this.selectedId = '';
    this.selected = null;
    this.detailError = '';
    this.detailLoading = false;
  }

  async refresh(): Promise<void> {
    await this.load();
    if (this.selectedId) await this.open(this.selectedId);
  }

  filteredPages(): WikiSummary[] {
    if (!this.filter) return this.pages;
    return this.pages.filter((page) =>
      [...(page.tags ?? []), ...(page.auto_tags ?? [])].includes(this.filter),
    );
  }
}

export const wiki = new WikiStore();
