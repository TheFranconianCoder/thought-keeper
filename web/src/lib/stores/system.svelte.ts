import { api } from '../api';
import { humanError } from '../api/errors';
import type { AtomDuplicates, AtomLayout, Finding, Pipeline, Status, VocabularyHealth } from '../api/types';
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
  pipeline = $state<Pipeline | null>(null);
  findings = $state<Finding[]>([]);
  findingsLoaded = $state(false);
  findingsError = $state('');

  layout = $state<AtomLayout | null>(null);
  duplicates = $state<AtomDuplicates | null>(null);
  vocabulary = $state<VocabularyHealth[] | null>(null);
  vaultLoaded = $state(false);

  notice = $state('');
  banner = $state<Banner | null>(null);

  private statusSeq = 0;
  private pipelineSeq = 0;
  private findingsSeq = 0;
  private vaultSeq = 0;

  async loadStatus(): Promise<void> {
    const seq = ++this.statusSeq;
    try {
      const status = await api.status();
      if (seq !== this.statusSeq) return;
      this.status = status;
      if (status.pipeline) this.pipeline = status.pipeline;
    } catch {
      if (seq === this.statusSeq && !this.status) this.status = null;
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

  async loadVault(): Promise<void> {
    const seq = ++this.vaultSeq;
    const [layout, duplicates, vocabulary] = await Promise.allSettled([
      api.atomLayout(),
      api.atomDuplicates(),
      api.vocabulary(),
    ]);
    if (seq !== this.vaultSeq) return;
    this.layout = layout.status === 'fulfilled' ? layout.value : null;
    this.duplicates = duplicates.status === 'fulfilled' ? duplicates.value : null;
    this.vocabulary = vocabulary.status === 'fulfilled' ? vocabulary.value.namespaces ?? [] : null;
    this.vaultLoaded = true;
  }

  async loadVocabulary(): Promise<void> {
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
