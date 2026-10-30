import { api } from '../api';
import type { Source, SourceFacets, SourceListFilter } from '../api/types';
import { readError, readSignal } from '../read';

/** Pages kept on each side of the visible range. The window is a cache, not a growing copy of the
 *  vault: scrolling through 10k sources must not retain 10k rows. */
const KEEP_PAGES = 3;

/** One request returns this many sources. A window is usually one or two pages; keeping the page
 *  modest keeps the per-page aggregates cheap on a large vault. */
const PAGE = 50;
const READ_TIMEOUT = 20_000;

/**
 * A source list reads a WINDOW.
 *
 * The list asks for the rows around the viewport (± overscan), and the server answers the filtered
 * total with the page. That total is the scroll height: the reader can jump to the end of a 200k
 * source vault without ever loading the rows in between.
 *
 * Three instances exist: the Sources lens (filters), the Bookmarks lens (`bookmarksOnly`) and the
 * Attention lens's failed sources (`failedOnly`). The store's old full list is no longer read by any
 * lens.
 */
class SourceWindowStore {
  /** The filtered total; the virtual list's height is count * rowHeight. */
  count = $state(0);
  loaded = $state(false);
  loading = $state(true);
  error = $state('');
  /** Bumped whenever the pages change, so `itemAt` callers become reactive. */
  version = $state(0);
  /** Bumped only when the pages are DISCARDED (filter, invalidation), never by a page arriving.
   *  Views listen to this to read the first page again even when the list is currently empty. */
  epoch = $state(0);

  facets = $state<SourceFacets>({ tags: [], attached: 0 });
  facetsLoaded = $state(false);
  facetsError = $state('');

  private filter: SourceListFilter = {};
  private filterKey = '';
  private pages = new Map<number, Source[]>();
  private runs = new Map<number, Promise<void>>();
  private seq = 0;
  private facetsRun: Promise<void> | null = null;
  private facetsRunSeq = -1;
  private facetsSeq = 0;
  /** A source whose delete is waiting for the server's gate. Its row becomes a placeholder instead
   *  of returning when the window is read again before the delete commits. */
  private hidden = new Set<string>();

  /** setFilter narrows the window. A changed filter throws the loaded pages away; the list asks for
   *  the new visible range as soon as the version moves. */
  setFilter(next: SourceListFilter): void {
    const filter: SourceListFilter = {
      kind: next.kind || undefined,
      query: next.query?.trim() || undefined,
      tag: next.tag || undefined,
      attachedOnly: next.attachedOnly ? true : undefined,
      bookmarksOnly: next.bookmarksOnly ? true : undefined,
      failedOnly: next.failedOnly ? true : undefined,
    };
    const key = JSON.stringify(filter);
    if (key === this.filterKey) return;
    this.filter = filter;
    this.filterKey = key;
    this.reset();
  }

  /** reset discards every page but keeps the filter and the last known total: the list shows
   *  placeholders at the right height until the new window arrives, never an empty flash. */
  reset(): void {
    this.seq++;
    this.pages.clear();
    this.runs.clear();
    this.loading = true;
    this.error = '';
    this.epoch++;
    this.version++;
  }

  /** invalidate is a reset plus "the tag cloud may be stale too". */
  invalidate(): void {
    this.reset();
    this.facetsSeq++;
    this.facetsLoaded = false;
    this.facetsError = '';
  }

  /** itemAt is undefined until the page that holds the row has arrived, and while a delete of that
   *  source is still waiting for the server. */
  itemAt(index: number): Source | undefined {
    void this.version;
    const page = Math.floor(index / PAGE);
    const source = this.pages.get(page)?.[index % PAGE];
    return source && !this.hidden.has(source.hash) ? source : undefined;
  }

  /** find looks in the loaded pages: the row actions need a source's label before opening it. */
  find(hash: string): Source | undefined {
    void this.version;
    if (this.hidden.has(hash)) return undefined;
    for (const page of this.pages.values()) {
      const source = page.find((item) => item.hash === hash);
      if (source) return source;
    }
    return undefined;
  }

  /** drop hides a source that is waiting for the delete to pass the pipeline gate. The row keeps its
   *  place (a placeholder) until the next read, so the indices of the other rows stay aligned. */
  drop(hash: string): void {
    if (!this.hidden.has(hash)) {
      this.hidden.add(hash);
      this.version++;
    }
  }

  /** reveal brings a source back when the delete failed (or after it committed and the list was read
   *  without it). */
  reveal(hash: string): void {
    if (this.hidden.delete(hash)) this.version++;
  }

  /** ensure makes the pages that cover [start, end) available. Pages already loaded or in flight are
   *  left alone; the caller may call it on every scroll tick. */
  async ensure(start: number, end: number): Promise<void> {
    if (end <= start) return;
    const first = Math.max(0, Math.floor(start / PAGE));
    const last = Math.max(first, Math.floor((end - 1) / PAGE));

    // Drop pages far from the visible range. The reader may scroll back, and then the page is read
    // again; keeping every page ever seen is what turns a window back into the whole list.
    let evicted = false;
    for (const page of [...this.pages.keys()]) {
      if (page < first - KEEP_PAGES || page > last + KEEP_PAGES) {
        this.pages.delete(page);
        evicted = true;
      }
    }
    if (evicted) this.version++;

    const jobs: Promise<void>[] = [];
    for (let page = first; page <= last; page++) {
      if (this.pages.has(page) || this.runs.has(page)) continue;
      jobs.push(this.fetchPage(page));
    }
    await Promise.all(jobs);
  }

  private async fetchPage(page: number): Promise<void> {
    const seq = this.seq;
    const run = (async () => {
      try {
        const result = await api.sources(
          this.filter,
          PAGE,
          page * PAGE,
          readSignal(READ_TIMEOUT),
        );
        if (seq !== this.seq) return;
        this.pages.set(page, result.sources ?? []);
        this.count = result.count ?? result.total ?? this.count;
        this.loaded = true;
        this.loading = false;
        this.error = '';
        this.version++;
      } catch (error) {
        if (seq !== this.seq) return;
        this.loaded = true;
        this.loading = false;
        this.error = readError(error);
        this.version++;
      } finally {
        if (seq === this.seq) this.runs.delete(page);
      }
    })();
    this.runs.set(page, run);
    return run;
  }

  /** loadFacets reads the vault-wide tag cloud and attached count. It never gates the list. */
  loadFacets(): Promise<void> {
    if (this.facetsRun && this.facetsRunSeq === this.facetsSeq) return this.facetsRun;
    const seq = this.facetsSeq;
    this.facetsRunSeq = seq;
    const run = (async () => {
      try {
        const facets = await api.sourceFacets(readSignal(READ_TIMEOUT));
        if (seq !== this.facetsSeq) return;
        this.facets = facets;
        this.facetsLoaded = true;
        this.facetsError = '';
      } catch (error) {
        if (seq !== this.facetsSeq) return;
        this.facetsLoaded = true;
        this.facetsError = readError(error);
      } finally {
        if (seq === this.facetsSeq) this.facetsRun = null;
      }
    })();
    this.facetsRun = run;
    return run;
  }
}

export const sourceWindow = new SourceWindowStore();
/** The Bookmarks lens: collected links only, still a window. */
export const bookmarkWindow = new SourceWindowStore();
/** The Attention lens's source half: sources with a failed step, still a window. */
export const failedWindow = new SourceWindowStore();
