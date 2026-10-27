import { api } from '../api';
import { ApiError, humanError } from '../api/errors';
import type { Atom, Source, SourceNode, SourceTopic } from '../api/types';
import type { LiveProgress } from '../source-state';
import { firstLine } from '../format';

const LOG_CAP = 128 * 1024;

/** PAGE is how many sources one request returns. The store walks the pages until the total. */
const PAGE = 200;

/** LOCAL_READ_TIMEOUT is how long one local read may take before it is abandoned. A local API
 *  answers in milliseconds; a read that has not answered in 20 seconds is a stuck connection.
 *  Waiting forever is what left a lens empty (or a source unopened) until a full PWA reload — the
 *  loader's promise never settled, so every later call joined a dead read. */
const LOCAL_READ_TIMEOUT = 20_000;
const DETAIL_READ_TIMEOUT = 45_000;
/** A read older than this is treated as stuck: its caller gets a fresh read instead of joining it.
 *  Joining forever is exactly the state that needed a full PWA reload to escape. */
const LIST_ABANDON_MS = 30_000;
const DETAIL_ABANDON_MS = 10_000;

function readSignal(ms: number): AbortSignal | undefined {
  return typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function'
    ? AbortSignal.timeout(ms)
    : undefined;
}

/** readError names a timeout for what it is instead of showing the browser's abort text. */
function readError(error: unknown): string {
  if (error instanceof DOMException && (error.name === 'AbortError' || error.name === 'TimeoutError')) {
    return 'The local service did not answer in time.';
  }
  return humanError(error);
}

/** SourceKindFilter is the source-list type selector. It lives in the store, not the view, so a
 *  filter survives a trip into a source and back (and a detour through another lens). */
export type SourceKindFilter = 'all' | 'md' | 'pdf' | 'txt' | 'url';

class SourcesStore {
  items = $state<Source[]>([]);
  count = $state(0);
  loaded = $state(false);
  error = $state('');

  /** The list's search term, type/tag filter, and whether only human-attached snapshots are shown.
   *  View state with the lifetime of a session. */
  query = $state('');
  kind = $state<SourceKindFilter>('all');
  filter = $state('');
  attachedOnly = $state(false);

  /** Live counters keyed by source hash, merged from `progress` events. */
  progress = $state<Record<string, LiveProgress>>({});

  selectedHash = $state('');
  selected = $state<Source | null>(null);
  tree = $state<SourceNode[]>([]);
  atoms = $state<Atom[]>([]);
  atomTotal = $state(0);
  atomTopics = $state<SourceTopic[]>([]);
  atomsError = $state('');
  log = $state('');
  logError = $state('');
  content = $state<string | null>(null);
  contentError = $state('');
  contentIsRendering = $state(false);
  detailLoading = $state(false);
  detailError = $state('');

  /** The Sub-sources tab: the sources this one owns (a sitemap's pages, a wiki page's URLs). */
  children = $state<Source[]>([]);
  childrenLoaded = $state(false);
  childrenError = $state('');

  /** The Summary tab: the source's own overview, fetched when the tab opens. */
  summary = $state('');
  summaryLoaded = $state(false);
  summaryLoading = $state(false);
  summaryGenerating = $state(false);
  /** summaryMissing says the file does not exist — the empty state, not an error to show in red. */
  summaryMissing = $state(false);
  summaryError = $state('');

  /** Bumped whenever something about the open source changed, so its tabs and part views reload. */
  version = $state(0);

  private listSeq = 0;
  /** listRun is the read in flight; calls during it join instead of starting a competing read. */
  private listRun: Promise<void> | null = null;
  private listRunAt = 0;
  private listAgain = false;
  private detailSeq = 0;
  /** detailRun is the read of the open source in flight; refreshes join it. */
  private detailRun: Promise<void> | null = null;
  private detailRunAt = 0;
  private detailRunHash = '';
  private detailAgain = false;

  /** loadList reads the whole list, once. A call that arrives while a read is running joins it and
   *  asks for one trailing read, which picks up what changed in the meantime.
   *
   *  The alternative — every call starting a new read and only the newest being allowed to publish —
   *  is what made a burst of source events (the summary backfill, a document finishing) starve the
   *  only read that would have filled the store: the lens stayed empty until a full PWA reload. */
  loadList(): Promise<void> {
    if (this.listRun) {
      // A read that has been in flight for a long time is stuck, not busy: leave it behind (the
      // sequence guard ignores whatever it eventually returns) and read again.
      if (Date.now() - this.listRunAt < LIST_ABANDON_MS) {
        this.listAgain = true;
        return this.listRun;
      }
      this.listRun = null;
      this.listAgain = false;
    }
    const run = this.runList();
    this.listRun = run;
    this.listRunAt = Date.now();
    void run
      .finally(() => {
        if (this.listRun === run) this.listRun = null;
      })
      .catch(() => {
        /* fetchList records its own error; the loader itself does not reject */
      });
    return run;
  }

  private async runList(): Promise<void> {
    try {
      await this.fetchList();
      // A failed FIRST read is retried once: the vault is local, so a timeout or a dropped
      // connection is transient far more often than it is a real answer.
      if (this.error && this.items.length === 0) {
        await new Promise((resolve) => setTimeout(resolve, 400));
        await this.fetchList();
      }
      // One bounded trailing read: enough to be current, not an open-ended loop under a storm.
      if (this.listAgain) {
        this.listAgain = false;
        await this.fetchList();
      }
    } finally {
      this.listAgain = false;
    }
  }

  private async fetchList(): Promise<void> {
    const seq = ++this.listSeq;
    try {
      // Read EVERY page first and publish the list once.
      //
      // Assigning page one and growing it made this shared store change its mind twice per load:
      // Sources with a filter briefly matched nothing, Bookmarks showed the bookmarks of the first
      // page only (a lone "1"), and the sidebar badges moved when an unrelated lens navigated. The
      // previous list stays on screen until the complete new one is here, so a refresh is invisible
      // and a failed refresh cannot turn an outage into an empty vault.
      const rows: Source[] = [];
      const seen = new Set<string>();
      let total = 0;
      for (;;) {
        const page = await api.sources('', PAGE, rows.length, readSignal(LOCAL_READ_TIMEOUT));
        if (seq !== this.listSeq) return;
        const batch = page.sources ?? [];
        let fresh = 0;
        for (const source of batch) {
          if (seen.has(source.hash)) continue;
          seen.add(source.hash);
          rows.push(source);
          fresh++;
        }
        total = Math.max(total, page.count ?? 0, page.total ?? 0, rows.length);
        // A source added between two requests shifts the offset; dedupe by hash so it cannot make
        // the loop run forever.
        if (batch.length < PAGE || rows.length >= total || fresh === 0) break;
      }
      this.items = rows;
      this.count = Math.max(total, rows.length);
      this.loaded = true;
      this.error = '';
    } catch (error) {
      if (seq !== this.listSeq) return;
      this.loaded = true;
      this.error = readError(error);
    }
  }

  async open(hash: string): Promise<void> {
    if (this.selectedHash === hash) {
      await this.refresh();
      return;
    }
    this.selectedHash = hash;
    this.selected = null;
    this.tree = [];
    this.atoms = [];
    this.atomTotal = 0;
    this.atomTopics = [];
    this.atomsError = '';
    this.log = '';
    this.logError = '';
    this.content = null;
    this.contentError = '';
    this.contentIsRendering = false;
    this.detailError = '';
    this.detailLoading = true;
    this.children = [];
    this.childrenLoaded = false;
    this.childrenError = '';
    this.summary = '';
    this.summaryLoaded = false;
    this.summaryLoading = false;
    this.summaryGenerating = false;
    this.summaryMissing = false;
    this.summaryError = '';
    await this.loadAll(hash, true);
  }

  async refresh(hash = this.selectedHash): Promise<void> {
    if (!hash) return;
    await this.loadAll(hash, false);
  }

  private async loadAll(hash: string, fresh: boolean): Promise<void> {
    // A refresh for the source that is already being read joins it. Starting a competing load and
    // letting only the newest publish is what left the detail on its skeleton during an event burst.
    if (this.detailRun && this.detailRunHash === hash) {
      // Join a read that is making progress; abandon one that has not answered for long enough to
      // be stuck. Joining forever was the "source will not open until a reload" state.
      if (Date.now() - this.detailRunAt < DETAIL_ABANDON_MS) {
        this.detailAgain = true;
        if (fresh) this.detailLoading = true;
        return this.detailRun;
      }
      this.detailRun = null;
      this.detailAgain = false;
    }
    const run = this.runDetail(hash, fresh);
    this.detailRun = run;
    this.detailRunAt = Date.now();
    this.detailRunHash = hash;
    this.detailAgain = false;
    try {
      await run;
    } finally {
      if (this.detailRun === run) {
        this.detailRun = null;
        this.detailRunHash = '';
        this.detailAgain = false;
      }
    }
  }

  private async runDetail(hash: string, fresh: boolean): Promise<void> {
    await this.fetchDetail(hash, fresh);
    // A first read that failed before it could show the source is retried once — a timeout on a
    // local service is transient, and "nothing happened" must not need a full PWA reload.
    if (!this.selected && this.detailError && this.selectedHash === hash) {
      await new Promise((resolve) => setTimeout(resolve, 400));
      if (this.selectedHash === hash) await this.fetchDetail(hash, false);
    }
    // One trailing read after a refresh asked during the first. The hash check keeps a run that a
    // switch to another source already superseded from consuming the new run's flag.
    if (this.detailRunHash === hash && this.detailAgain && this.selectedHash === hash) {
      this.detailAgain = false;
      await this.fetchDetail(hash, false);
    }
  }

  private async fetchDetail(hash: string, fresh: boolean): Promise<void> {
    const seq = ++this.detailSeq;
    const still = (): boolean => seq === this.detailSeq && this.selectedHash === hash;
    if (fresh) this.detailLoading = true;

    try {
      // The header first, then ONE read at a time. The browser allows six connections per host and
      // the event stream is one of them: five concurrent detail reads left no room for the list (or
      // the next detail), so one slow file froze the screen until a reload. Local reads are
      // milliseconds; sequencing them costs nothing and keeps the app responsive behind a slow one.
      const meta = await api.source(hash, readSignal(LOCAL_READ_TIMEOUT));
      if (!still()) return;
      this.selected = meta;
      this.detailError = '';
      this.version++;
      this.detailLoading = false;

      try {
        const tree = await api.sourceTree(hash, readSignal(LOCAL_READ_TIMEOUT));
        if (still()) this.tree = tree.nodes ?? [];
      } catch {
        if (still()) this.tree = [];
      }

      try {
        const atoms = await api.sourceAtoms(hash, readSignal(LOCAL_READ_TIMEOUT));
        if (!still()) return;
        this.atoms = atoms.atoms ?? [];
        this.atomTotal = atoms.total ?? this.atoms.length;
        this.atomTopics = atoms.topics ?? [];
        this.atomsError = '';
      } catch (error) {
        if (!still()) return;
        this.atoms = [];
        this.atomTotal = 0;
        this.atomTopics = [];
        this.atomsError = firstLine(readError(error));
      }

      try {
        const text = await api.sourceLog(hash, readSignal(LOCAL_READ_TIMEOUT));
        if (!still()) return;
        this.log = text;
        this.logError = '';
      } catch (error) {
        if (!still()) return;
        this.log = '';
        this.logError = firstLine(readError(error));
      }

      const which = meta.ext === '.pdf' ? 'render' : undefined;
      this.contentIsRendering = which === 'render';
      try {
        const text = await api.rawText(hash, which, readSignal(DETAIL_READ_TIMEOUT));
        if (!still()) return;
        // Re-setting identical bytes would re-render the whole Markdown tree. Keeping the value
        // stable is what stops a background event from flashing the page.
        if (this.content !== text) this.content = text;
        this.contentError = '';
      } catch (error) {
        if (!still()) return;
        if (this.content === null) {
          this.contentError = firstLine(readError(error));
          this.content = which === 'render' ? '' : null;
        }
      }
    } catch (error) {
      if (!still()) return;
      this.selected = null;
      this.detailError = firstLine(readError(error));
      this.detailLoading = false;
    }
  }

  async nodeText(hash: string, nodeID: string): Promise<string> {
    const node = await api.sourceNode(hash, nodeID, readSignal(LOCAL_READ_TIMEOUT));
    return node.text ?? '';
  }

  async rawFile(hash: string, which: string): Promise<string> {
    return api.rawText(hash, which, readSignal(LOCAL_READ_TIMEOUT));
  }

  /** setTags replaces a source's human labels and mirrors the answer into the open detail and the
   *  row, so the rail does not have to wait for the source event to catch up. */
  async setTags(hash: string, tags: string[]): Promise<void> {
    const updated = await api.setSourceTags(hash, tags);
    const row = this.items.find((source) => source.hash === hash);
    if (row) {
      row.tags = updated.tags ?? [];
      row.auto_tags = updated.auto_tags ?? [];
    }
    if (this.selectedHash === hash && this.selected) {
      this.selected = {
        ...this.selected,
        tags: updated.tags ?? [],
        auto_tags: updated.auto_tags ?? [],
      };
    }
    this.version++;
  }

  /** loadChildren reads the Sub-sources tab. `force` re-reads after a source event. */
  async loadChildren(hash: string, force = false): Promise<void> {
    if (!hash) return;
    if (!force && this.childrenLoaded) return;
    try {
      const result = await api.sourceChildren(hash, readSignal(LOCAL_READ_TIMEOUT));
      if (this.selectedHash !== hash) return;
      this.children = result.sources ?? [];
      this.childrenError = '';
    } catch (error) {
      if (this.selectedHash !== hash) return;
      this.children = [];
      this.childrenError = firstLine(readError(error));
    } finally {
      if (this.selectedHash === hash) this.childrenLoaded = true;
    }
  }

  /** loadSummary reads the Summary tab. A 404 means the file does not exist (yet). */
  async loadSummary(hash: string, force = false): Promise<void> {
    if (!hash) return;
    if (!force && this.summaryLoaded) return;
    this.summaryLoading = true;
    try {
      this.summary = await api.sourceSummary(hash, readSignal(LOCAL_READ_TIMEOUT));
      this.summaryMissing = false;
      this.summaryError = '';
    } catch (error) {
      if (this.selectedHash !== hash) return;
      this.summary = '';
      // A source without a summary answers 404: that is the empty state, not a failure to show.
      this.summaryMissing = error instanceof ApiError && error.status === 404;
      this.summaryError = this.summaryMissing ? '' : firstLine(readError(error));
    } finally {
      if (this.selectedHash === hash) {
        this.summaryLoaded = true;
        this.summaryLoading = false;
        this.summaryGenerating = false;
      }
    }
  }

  /** generateSummary asks the app for a summary run. The result arrives as a source event. */
  async generateSummary(hash: string): Promise<void> {
    if (!hash || this.summaryGenerating) return;
    this.summaryGenerating = true;
    try {
      await api.generateSourceSummary(hash);
    } catch (error) {
      this.summaryGenerating = false;
      this.summaryError = firstLine(humanError(error));
    }
  }

  /** drop removes a source from the visible list NOW. A confirmed deletion returns to the list and
   *  must not wait for the server (which may be waiting for the gate): the row leaves at once. A
   *  failed delete reloads the list, which brings the row back — optimism a reload can correct. */
  drop(hash: string): void {
    this.items = this.items.filter((source) => source.hash !== hash);
    this.count = Math.max(0, this.count - 1);
  }

  clearSelection(): void {    this.detailSeq++;
    // Drop the claim on an in-flight read: leaving the source must not make the NEXT open join a
    // read that was started for the previous visit and never finished.
    this.detailRun = null;
    this.detailRunHash = '';
    this.detailAgain = false;
    this.selectedHash = '';
    this.selected = null;
    this.tree = [];
    this.atoms = [];
    this.atomTotal = 0;
    this.atomTopics = [];
    this.log = '';
    this.content = null;
    this.detailError = '';
    this.detailLoading = false;
    this.children = [];
    this.childrenLoaded = false;
    this.childrenError = '';
    this.summary = '';
    this.summaryLoaded = false;
    this.summaryLoading = false;
    this.summaryGenerating = false;
    this.summaryMissing = false;
    this.summaryError = '';
  }

  applyProgress(hash: string, frame: Omit<LiveProgress, 'fields'> & { fields: Record<string, number> }): void {
    const previous = this.progress[hash];
    this.progress[hash] = {
      phase: frame.phase ?? previous?.phase,
      msg: frame.msg ?? previous?.msg,
      fields: { ...(previous?.fields ?? {}), ...frame.fields },
    };
  }

  appendLog(hash: string, line: string): void {
    if (this.selectedHash !== hash || !line) return;
    const next = this.log + line;
    this.log = next.length > LOG_CAP ? next.slice(-LOG_CAP) : next;
  }

  markRunning(hash: string, step: string): void {
    const item = this.items.find((source) => source.hash === hash);
    if (item) item.running_step = step;
  }

  find(hash: string): Source | undefined {
    return this.items.find((source) => source.hash === hash);
  }

  /** applySourcePatch is for a synchronous result that already knows the new row. */
  patch(hash: string, patch: Partial<Source>): void {
    const item = this.items.find((source) => source.hash === hash);
    if (item) Object.assign(item, patch);
  }
}

export const sources = new SourcesStore();
