import { api } from '../api';
import { humanError } from '../api/errors';
import type { AskSummary, Run, SavedAsk } from '../api/types';
import { askPath, navigate, router } from '../router.svelte';
import { firstLine } from '../format';
import { t } from '../i18n/index.svelte';

const RUN_POLL_MS = 900;
const RUN_TIMEOUT_MS = 5 * 60_000;

export type PendingStatus = 'starting' | 'running' | 'failed';

/**
 * PendingAsk is one question in flight. It is rendered in the SAME list as the saved queries: the
 * reader sees the question and its status where the answer will appear, and may ask the next
 * question while this one is still being answered (Ask is never gated, and runs are parallel).
 */
export interface PendingAsk {
  /** A local key: the run id does not exist until POST /query answers. */
  key: string;
  runId: string;
  question: string;
  status: PendingStatus;
  error?: string;
  startedAt: number;
}

class AsksStore {
  items = $state<AskSummary[]>([]);
  count = $state(0);
  loaded = $state(false);
  error = $state('');

  selectedId = $state('');
  selected = $state<SavedAsk | null>(null);
  detailLoading = $state(false);
  detailError = $state('');

  /** Questions in flight, newest first. A failed run stays until it is dismissed or retried. */
  pending = $state<PendingAsk[]>([]);

  private listSeq = 0;
  private detailSeq = 0;
  private localSeq = 0;
  private finishing = new Set<string>();

  /** busy is true while at least one question is being answered. */
  get busy(): boolean {
    return this.pending.some((run) => run.status !== 'failed');
  }

  async load(): Promise<void> {
    const seq = ++this.listSeq;
    try {
      const result = await api.asks();
      if (seq !== this.listSeq) return;
      this.items = result.asks ?? [];
      this.count = result.count ?? this.items.length;
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
      const ask = await api.ask(id);
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.selected = ask;
      this.detailError = '';
    } catch (error) {
      if (seq !== this.detailSeq || this.selectedId !== id) return;
      this.selected = null;
      this.detailError = firstLine(humanError(error));
    } finally {
      if (seq === this.detailSeq) this.detailLoading = false;
    }
  }

  clearSelection(): void {
    this.detailSeq++;
    this.selectedId = '';
    this.selected = null;
    this.detailError = '';
    this.detailLoading = false;
  }

  /** submit starts one question. It never waits for another one: answers run in parallel. */
  async submit(question: string): Promise<void> {
    const value = question.trim();
    if (!value) return;

    // The detail is a saved answer; a new question belongs to the list, where its status is visible.
    if (router.current.view === 'ask' && router.current.id) navigate('#/ask');

    const key = `q-${++this.localSeq}`;
    this.pending = [
      { key, runId: '', question: value, status: 'starting', startedAt: Date.now() },
      ...this.pending,
    ];

    let runID = '';
    try {
      runID = (await api.query(value)).run_id;
    } catch (error) {
      this.patch(key, {
        status: 'failed',
        error: t('ask.failedToAsk', { error: firstLine(humanError(error)) }),
      });
      return;
    }
    // The run frame can arrive before POST /query returned its id. The poller below fetches once
    // immediately, so an early finish is still seen; observeRun() handles the later frames.
    this.patch(key, { runId: runID, status: 'running' });
    void this.watch(key, runID);
  }

  /** observeRun is the SSE path to the same completion the poller watches. Both are idempotent. */
  observeRun(runID: string): void {
    if (!runID) return;
    const pending = this.pending.find((run) => run.runId === runID);
    if (pending) void this.finish(pending.key, runID);
  }

  /** retry asks a failed question again, as a new run. */
  retry(key: string): void {
    const failed = this.pending.find((run) => run.key === key);
    if (!failed) return;
    this.drop(key);
    void this.submit(failed.question);
  }

  /** dismiss removes one failed row; a running row is not dismissible. */
  dismiss(key: string): void {
    this.drop(key);
  }

  private patch(key: string, changes: Partial<PendingAsk>): void {
    this.pending = this.pending.map((run) => (run.key === key ? { ...run, ...changes } : run));
  }

  private drop(key: string): void {
    this.pending = this.pending.filter((run) => run.key !== key);
  }

  private async watch(key: string, runID: string): Promise<void> {
    const deadline = Date.now() + RUN_TIMEOUT_MS;
    while (this.pending.some((run) => run.key === key) && Date.now() < deadline) {
      try {
        const run = await api.run(runID);
        if (run.status !== 'running') {
          await this.finish(key, runID, run);
          return;
        }
      } catch {
        /* the poll is a safety net; a failed read simply tries again */
      }
      await new Promise((resolve) => setTimeout(resolve, RUN_POLL_MS));
    }
    if (this.pending.some((run) => run.key === key)) {
      this.patch(key, { status: 'failed', error: t('ask.takingLong') });
    }
  }

  private async finish(key: string, runID: string, known?: Run): Promise<void> {
    if (this.finishing.has(runID)) return;
    this.finishing.add(runID);
    try {
      const run = known ?? (await api.run(runID));
      await this.applyRun(key, run);
    } catch (error) {
      this.patch(key, {
        status: 'failed',
        error: t('ask.failedToRead', { error: firstLine(humanError(error)) }),
      });
    } finally {
      this.finishing.delete(runID);
    }
  }

  private async applyRun(key: string, run: Run): Promise<void> {
    if (!this.pending.some((pending) => pending.key === key)) return;
    if (run.status !== 'succeeded') {
      const reason = run.error ? `: ${firstLine(run.error)}` : '';
      this.patch(key, { status: 'failed', error: t('ask.runStatus', { status: run.status, error: reason }) });
      return;
    }

    // The ask file is written just after the run row finishes. One short retry closes that window;
    // a list that still misses it gets a synthetic row, and the next read replaces it with the file.
    await this.load();
    if (!this.items.some((ask) => ask.id === run.id)) {
      await new Promise((resolve) => setTimeout(resolve, 250));
      await this.load();
    }
    if (!this.items.some((ask) => ask.id === run.id)) {
      const pending = this.pending.find((item) => item.key === key);
      const now = new Date().toISOString();
      this.items = [
        {
          id: run.id,
          question: run.question || pending?.question || '',
          created_at: run.started_at || now,
          updated_at: run.ended_at || now,
        },
        ...this.items,
      ];
      this.count = Math.max(this.count, this.items.length);
    }

    this.drop(key);
    if (this.selectedId === run.id) void this.open(run.id);
  }

  async remove(id: string): Promise<void> {
    await api.deleteAsk(id);
    if (this.selectedId === id) this.clearSelection();
    await this.load();
  }

  async refresh(): Promise<void> {
    await this.load();
    if (this.selectedId) await this.open(this.selectedId);
  }
}

export const asks = new AsksStore();
