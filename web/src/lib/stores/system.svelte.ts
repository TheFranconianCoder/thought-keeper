import { api } from '../api';
import { humanError } from '../api/errors';
import type { AtomDuplicates, Finding, Pipeline, Status, VocabularyHealth } from '../api/types';
import { t } from '../i18n/index.svelte';
import { toast } from '../toast.svelte';

/** A local action that started before the server's first SSE run frame arrived. The top bar shows
 *  it so a click looks pressed immediately. */
export interface Banner {
  op: string;
  status: string;
}

class SystemStore {
  status = $state<Status | null>(null);
  /** statusError is the last failed doctor-report read. A null status without an error is "not
   *  read yet" (the lens paints its skeleton); with one it is a read that failed. */
  statusError = $state('');
  pipeline = $state<Pipeline | null>(null);
  findings = $state<Finding[]>([]);
  findingsLoaded = $state(false);
  findingsError = $state('');

  duplicates = $state<AtomDuplicates | null>(null);
  vocabulary = $state<VocabularyHealth[] | null>(null);
  vaultLoaded = $state(false);

  notice = $state('');
  banner = $state<Banner | null>(null);

  private statusSeq = 0;
  private statusRun: Promise<void> | null = null;
  private statusAgain = false;
  private pipelineSeq = 0;
  private findingsSeq = 0;
  private vaultRun: Promise<void> | null = null;
  private vocabularyRun: Promise<void> | null = null;

  /** loadStatus reads the doctor report. A read already in flight is JOINED, not restarted: the
   *  front door, the sidebar badges and every source/cluster/run event ask for the same report, and
   *  starting a competing read per event is what made the counts arrive seconds late during a run.
   *  An ask that lands while a read runs sets one trailing read, so the last event of a burst is
   *  still reflected without a read per event. */
  loadStatus(): Promise<void> {
    if (this.statusRun) {
      this.statusAgain = true;
      return this.statusRun;
    }
    const run = this.runStatus();
    this.statusRun = run;
    return run;
  }

  private async runStatus(): Promise<void> {
    try {
      await this.fetchStatus();
      if (this.statusAgain) {
        this.statusAgain = false;
        await this.fetchStatus();
      }
    } finally {
      this.statusRun = null;
      this.statusAgain = false;
    }
  }

  private async fetchStatus(): Promise<void> {
    const seq = ++this.statusSeq;
    try {
      const status = await api.status();
      if (seq !== this.statusSeq) return;
      this.status = status;
      this.statusError = '';
      if (status.pipeline) this.pipeline = status.pipeline;
    } catch (error) {
      if (seq !== this.statusSeq) return;
      // The last known report stays on screen: a failed refresh is not an empty vault.
      this.statusError = humanError(error);
    }
  }

  async loadPipeline(): Promise<void> {
    const seq = ++this.pipelineSeq;
    try {
      const pipeline = await api.pipeline();
      if (seq === this.pipelineSeq) this.pipeline = pipeline;
    } catch {
      /* the status is a convenience; a failed read leaves the last known one */
    }
  }

  async loadFindings(): Promise<void> {
    const seq = ++this.findingsSeq;
    try {
      const result = await api.findings();
      if (seq !== this.findingsSeq) return;
      this.findings = result.findings ?? [];
      this.findingsLoaded = true;
      this.findingsError = '';
    } catch (error) {
      if (seq !== this.findingsSeq) return;
      this.findings = [];
      this.findingsLoaded = true;
      this.findingsError = humanError(error);
    }
  }

  /** loadVault reads the two reports behind the Status lens's vault section. A read in flight is
   *  JOINED, not restarted: the lens and the catch-up both ask when the page opens, and a second
   *  scan of the vault is what made the vocabulary arrive after everything else. Unlike the doctor
   *  report there is no trailing read — the report is a cache with its own refresh behind the
   *  reader, and the extra ask at page open would buy nothing. */
  loadVault(): Promise<void> {
    if (!this.vaultRun) {
      this.vaultRun = this.fetchVault().finally(() => {
        this.vaultRun = null;
      });
    }
    return this.vaultRun;
  }

  private async fetchVault(): Promise<void> {
    const [duplicates, vocabulary] = await Promise.allSettled([
      api.atomDuplicates(),
      api.vocabulary(),
    ]);
    this.duplicates = duplicates.status === 'fulfilled' ? duplicates.value : null;
    this.vocabulary = vocabulary.status === 'fulfilled' ? vocabulary.value.namespaces ?? [] : null;
    this.vaultLoaded = true;
  }

  /** loadVocabulary re-reads the name catalogs after a vocabulary event; a read in flight is joined. */
  loadVocabulary(): Promise<void> {
    if (!this.vocabularyRun) {
      this.vocabularyRun = this.fetchVocabulary().finally(() => {
        this.vocabularyRun = null;
      });
    }
    return this.vocabularyRun;
  }

  private async fetchVocabulary(): Promise<void> {
    try {
      const result = await api.vocabulary();
      this.vocabulary = result.namespaces ?? [];
    } catch {
      this.vocabulary = null;
    }
  }

  markBanner(op: string): void {
    this.banner = { op, status: 'running' };
  }

  clearBanner(): void {
    this.banner = null;
  }

  /** finishRun is called for a `run` event whose status is no longer running. */
  finishRun(op: string, status: string, error?: string): void {
    this.banner = null;
    if (status === 'failed') {
      const reason = (error ?? '').split('\n')[0].trim();
      toast(
        t('status.runFailed', {
          op: op || t('status.run'),
          reason: reason ? `: ${reason}` : '',
        }),
        'error',
      );
    }
  }

  /** applyPipeline uses the full status carried by an SSE frame — no request is necessary. */
  applyPipeline(pipeline: Pipeline): void {
    this.pipeline = pipeline;
  }
}

export const system = new SystemStore();
