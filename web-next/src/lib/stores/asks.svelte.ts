import { api } from '../api';
import { humanError } from '../api/errors';
import type { AskSummary, Run, SavedAsk } from '../api/types';
import { askPath, navigate, router } from '../router.svelte';
import { firstLine } from '../format';
import { system } from './system.svelte';

const RUN_POLL_MS = 900;
const RUN_TIMEOUT_MS = 5 * 60_000;

class AsksStore {
  items = $state<AskSummary[]>([]);
  count = $state(0);
  loaded = $state(false);
  error = $state('');

  selectedId = $state('');
  selected = $state<SavedAsk | null>(null);
  detailLoading = $state(false);
  detailError = $state('');

  activeRunId = $state('');
  currentId = $state('');
  question = $state('');
  answer = $state('');
  notice = $state('');
  busy = $state(false);

  private listSeq = 0;
  private detailSeq = 0;
  private finished = new Set<string>();

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

  /** clear empties the live answer without touching the saved queries in the vault. */
  clear(): void {
    this.activeRunId = '';
    this.currentId = '';
    this.question = '';
    this.answer = '';
    this.notice = '';
    this.busy = false;
    system.clearBanner();
  }

  async submit(question: string): Promise<void> {
    const value = question.trim();
    if (!value || this.busy) return;

    if (router.current.view === 'ask' && router.current.id) navigate('#/ask');
    this.clear();
    this.busy = true;
    this.question = value;
    this.notice = 'Searching the claims…';
    system.markBanner('query');

    let runID = '';
    try {
      runID = (await api.query(value)).run_id;
      this.activeRunId = runID;
    } catch (error) {
      this.notice = `Could not ask: ${firstLine(humanError(error))}`;
      this.busy = false;
      system.clearBanner();
      return;
    }
    void this.watch(runID);
  }

  /** observeRun is the SSE path to the same completion the poller watches. Both are idempotent. */
  observeRun(runID: string): void {
    if (!runID || !this.busy) return;
    // The run frame can arrive before POST /query returned its id. Adopt it in that window; the
    // poller would otherwise be the only path to the answer.
    if (!this.activeRunId) this.activeRunId = runID;
    if (this.activeRunId !== runID) return;
    void this.finish(runID);
  }

  private async watch(runID: string): Promise<void> {
    const deadline = Date.now() + RUN_TIMEOUT_MS;
    while (this.busy && this.activeRunId === runID && Date.now() < deadline) {
      try {
        const run = await api.run(runID);
        if (run.status !== 'running') {
          await this.applyRun(runID, run);
          return;
        }
      } catch {
        /* the poll is a safety net; a failed read simply tries again */
      }
      await new Promise((resolve) => setTimeout(resolve, RUN_POLL_MS));
    }
    if (this.busy && this.activeRunId === runID && Date.now() >= deadline) {
      this.notice = 'The answer is taking unusually long. It may still finish in the background.';
      this.busy = false;
      system.clearBanner();
    }
  }

  private async finish(runID: string): Promise<void> {
    if (!this.busy || this.activeRunId !== runID) return;
    try {
      const run = await api.run(runID);
      await this.applyRun(runID, run);
    } catch (error) {
      this.notice = `Could not read the answer: ${firstLine(humanError(error))}`;
      this.busy = false;
      system.clearBanner();
    }
  }

  private async applyRun(runID: string, run: Run): Promise<void> {
    if (this.finished.has(runID)) return;
    this.finished.add(runID);

    if (!this.question && run.question) this.question = run.question;
    this.busy = false;
    system.clearBanner();

    if (run.status !== 'succeeded') {
      this.notice = `Query ${run.status}${run.error ? `: ${firstLine(run.error)}` : ''}`;
      return;
    }

    this.answer = run.answer || '(The model returned an empty answer.)';
    // The answer lives on the saved-query detail now; the list keeps no transient result or run
    // statistics, so returning to it is a clean history list.
    this.notice = '';
    this.currentId = runID;
    await this.load();

    // A finished answer is a saved query detail, not the history list. Navigate only while the reader
    // is in Ask: a background answer must not steal the page from another lens.
    if (router.current.view === 'ask') navigate(askPath(runID));
  }

  async remove(id: string): Promise<void> {
    await api.deleteAsk(id);
    if (this.selectedId === id) this.clearSelection();
    if (this.currentId === id) {
      this.activeRunId = '';
      this.currentId = '';
      this.question = '';
      this.answer = '';
      this.notice = '';
    }
    await this.load();
  }

  async refresh(): Promise<void> {
    await this.load();
    if (this.selectedId) await this.open(this.selectedId);
  }
}

export const asks = new AsksStore();
