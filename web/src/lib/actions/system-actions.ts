import { api } from '../api';
import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine } from '../format';
import { t, tn } from '../i18n/index.svelte';
import { claims } from '../stores/claims.svelte';
import { bookmarkWindow, failedWindow, sourceWindow } from '../stores/source-window.svelte';
import { system } from '../stores/system.svelte';
import { toast } from '../toast.svelte';
import { waitForRun } from './runs';

/** A vault-wide repair changed the sources: every window is thrown away, and the mounted lens reads
 *  its visible range again. */
function refreshSourceData(): Promise<void> {
  sourceWindow.invalidate();
  bookmarkWindow.invalidate();
  failedWindow.invalidate();
  return Promise.resolve();
}

export async function clusterClaims(): Promise<void> {
  try {
    const result = await api.cluster();
    toast(result.queued ? t('status.clusterQueued') : t('status.clusterNothing'), 'success');
    await Promise.all([refreshSourceData(), claims.refresh(), system.loadStatus()]);
  } catch (error) {
    toast(t('status.clusterFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function reconcileVault(): Promise<void> {
  system.notice = t('status.reconcileNotice');
  try {
    const result = await api.reconcile();
    const bits = [t('status.checked', { count: result.checked })];
    if (result.invalidated?.length) {
      bits.push(t('status.invalidated', { count: result.invalidated.length }));
    }
    if (result.broken?.length) bits.push(t('status.broken', { count: result.broken.length }));
    if (result.languages) bits.push(tn('status.languagesDetected', result.languages));
    if (result.identities) bits.push(tn('status.identities', result.identities));
    if (result.ownerships) bits.push(tn('status.owners', result.ownerships));
    toast(
      t('status.consistency', { bits: bits.join(' · ') }),
      result.broken?.length ? 'warning' : 'success',
    );
    await Promise.all([refreshSourceData(), system.loadFindings(), system.loadStatus()]);
  } catch (error) {
    toast(t('status.reconcileFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function reindexVault(): Promise<void> {
  system.notice = t('status.reindexNotice');
  try {
    const result = await api.reindex();
    const bits = [tn('status.claims', result.atoms), tn('status.clusterCount', result.clusters)];
    if (result.skipped) bits.push(t('status.skipped', { count: result.skipped }));
    if (result.orphans) bits.push(tn('status.orphanRows', result.orphans));
    if (result.atoms_orphaned) bits.push(tn('status.trashed', result.atoms_orphaned));
    if (result.provenance_pruned) bits.push(tn('status.deadSource', result.provenance_pruned));
    if (result.clusters_removed) bits.push(tn('status.emptyClusters', result.clusters_removed));
    toast(t('status.reindexDone', { bits: bits.join(' · ') }), 'success');
    await Promise.all([refreshSourceData(), system.loadFindings(), system.loadStatus()]);
    if (system.vaultLoaded) await system.loadVault();
  } catch (error) {
    toast(t('status.reindexFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function rebuildAllTrees(): Promise<void> {
  system.notice = t('status.outlinesNotice');
  try {
    const result = await api.rebuildTrees();
    toast(
      t('status.outlinesRebuilt', {
        built: result.built,
        sources: result.sources,
        failed: result.failed ? t('status.failedCount', { count: result.failed }) : '',
      }),
      result.failed ? 'warning' : 'success',
    );
    await Promise.all([refreshSourceData(), system.loadStatus()]);
  } catch (error) {
    toast(t('status.outlinesFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function runLint(): Promise<void> {
  system.notice = t('status.lintNotice');
  system.markBanner('lint');
  try {
    const { run_id } = await api.lint();
    const run = await waitForRun(run_id);
    await system.loadFindings();
    system.notice =
      run?.status === 'succeeded'
        ? run.answer || tn('status.findings', system.findings.length)
        : run
          ? t('status.lintStatus', {
              status: run.status,
              error: run.error ? `: ${firstLine(run.error)}` : '',
            })
          : t('status.lintTimeout');
  } catch (error) {
    system.notice = t('status.lintFailed', { error: firstLine(humanError(error)) });
  } finally {
    system.clearBanner();
  }
}

export async function cleanupClusters(): Promise<void> {
  system.notice = t('status.cleanupNotice');
  try {
    const result = await api.cleanup();
    toast(
      t('status.cleanupDone', {
        pruned: tn('status.clustersPruned', result.clusters_pruned),
        removed: tn('status.clustersRemoved', result.clusters_removed),
        findings: tn('status.findings', result.findings),
      }),
      'success',
    );
    await Promise.all([system.loadFindings(), refreshSourceData(), system.loadStatus()]);
  } catch (error) {
    toast(t('status.cleanupFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function curateVocabulary(): Promise<void> {
  try {
    await api.curateVocabulary();
    toast(t('status.curateStarted'), 'info');
  } catch (error) {
    toast(t('status.curateFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function retopicClaims(): Promise<void> {
  try {
    await api.retopicClaims();
    toast(t('status.retopicStarted'), 'info');
  } catch (error) {
    toast(t('status.retopicFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function reorganizeClaims(): Promise<void> {
  try {
    const result = await api.reorganizeAtoms();
    toast(
      result.moved === 0
        ? t('status.reorganizeNothing')
        : t('status.reorganizeMoved', {
            claims: tn('status.claims', result.moved),
            folders: tn('status.folders', result.folders_removed ?? 0),
          }),
      'success',
    );
    await system.loadVault();
  } catch (error) {
    toast(t('status.reorganizeFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function quoteClaimCode(): Promise<void> {
  system.notice = t('status.quoteNotice');
  try {
    const result = await api.quoteClaimCode();
    toast(
      result.repaired === 0
        ? t('status.quoteNothing')
        : t('status.quoted', {
            spans: tn('status.bracketSpans', result.fragments),
            claims: tn('status.claims', result.repaired),
            skipped: result.skipped
              ? ` · ${t('status.skipped', { count: result.skipped })}`
              : '',
          }),
      result.skipped ? 'warning' : 'success',
    );
    await Promise.all([claims.refresh(), system.loadFindings(), system.loadStatus()]);
    if (system.vaultLoaded) await system.loadVault();
  } catch (error) {
    toast(t('status.quoteFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function collapseDuplicateClaims(): Promise<void> {
  const duplicates = system.duplicates;
  if (duplicates && duplicates.groups > 0) {
    const confirmed = await confirm({
      title: tn('status.collapseTitle', duplicates.groups),
      message: t('status.collapseMessage'),
      confirmLabel: t('status.collapseConfirm'),
    });
    if (!confirmed) return;
  }
  try {
    const result = await api.collapseAtoms();
    toast(
      result.collapsed === 0
        ? t('status.collapseNothing')
        : t('status.collapseKept', {
            groups: tn('status.groupCount', result.groups),
            files: tn('status.files', result.collapsed),
          }),
      'success',
    );
    await system.loadVault();
  } catch (error) {
    toast(t('status.collapseFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}
