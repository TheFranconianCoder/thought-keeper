import { api } from '../api';
import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine, plural } from '../format';
import { claims } from '../stores/claims.svelte';
import { sources } from '../stores/sources.svelte';
import { system } from '../stores/system.svelte';
import { toast } from '../toast.svelte';
import { waitForRun } from './runs';

export async function clusterClaims(): Promise<void> {
  try {
    const result = await api.cluster();
    toast(
      result.queued
        ? 'Cluster pass queued — it waits until no document step is running.'
        : 'Nothing to cluster — every claim is associated.',
      'success',
    );
    await Promise.all([sources.loadList(), claims.refresh(), system.loadStatus()]);
  } catch (error) {
    toast(`Cluster failed: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function reconcileVault(): Promise<void> {
  system.notice = 'Checking the vault…';
  try {
    const result = await api.reconcile();
    const bits = [`checked ${result.checked}`];
    if (result.invalidated?.length) bits.push(`${result.invalidated.length} invalidated`);
    if (result.broken?.length) bits.push(`${result.broken.length} broken`);
    if (result.languages) bits.push(`${plural(result.languages, 'language')} detected`);
    if (result.identities) bits.push(`${plural(result.identities, 'identity')} restored`);
    if (result.ownerships) bits.push(`${plural(result.ownerships, 'owner')} restored`);
    toast(`Consistency: ${bits.join(' · ')}.`, result.broken?.length ? 'warning' : 'success');
    await Promise.all([sources.loadList(), system.loadFindings(), system.loadStatus()]);
  } catch (error) {
    toast(`Consistency check failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function reindexVault(): Promise<void> {
  system.notice = 'Rebuilding the index from the vault…';
  try {
    const result = await api.reindex();
    const bits = [`${plural(result.atoms, 'claim')}`, `${plural(result.clusters, 'cluster')}`];
    if (result.skipped) bits.push(`${result.skipped} skipped`);
    if (result.orphans) bits.push(`${plural(result.orphans, 'orphan row')} removed`);
    if (result.atoms_orphaned) bits.push(`${plural(result.atoms_orphaned, 'claim')} trashed`);
    if (result.provenance_pruned) bits.push(`${plural(result.provenance_pruned, 'claim')} lost a dead source`);
    if (result.clusters_removed) bits.push(`${plural(result.clusters_removed, 'empty cluster')} removed`);
    toast(`Reindex complete: ${bits.join(' · ')}.`, 'success');
    await Promise.all([sources.loadList(), system.loadFindings(), system.loadStatus()]);
    if (system.vaultLoaded) await system.loadVault();
  } catch (error) {
    toast(`Reindex failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function rebuildAllTrees(): Promise<void> {
  system.notice = 'Rebuilding every outline…';
  try {
    const result = await api.rebuildTrees();
    toast(
      `Outlines rebuilt: ${result.built}/${result.sources}${result.failed ? ` · ${result.failed} failed` : ''}.`,
      result.failed ? 'warning' : 'success',
    );
    await Promise.all([sources.loadList(), system.loadStatus()]);
  } catch (error) {
    toast(`Outline rebuild failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function runLint(): Promise<void> {
  system.notice = 'Running the health checks…';
  system.markBanner('lint');
  try {
    const { run_id } = await api.lint();
    const run = await waitForRun(run_id);
    await system.loadFindings();
    system.notice =
      run?.status === 'succeeded'
        ? run.answer || `${plural(system.findings.length, 'finding')}`
        : run
          ? `Lint ${run.status}${run.error ? `: ${firstLine(run.error)}` : ''}`
          : 'Lint did not finish in time.';
  } catch (error) {
    system.notice = `Lint failed: ${firstLine(humanError(error))}`;
  } finally {
    system.clearBanner();
  }
}

export async function cleanupClusters(): Promise<void> {
  system.notice = 'Cleaning up the association layer…';
  try {
    const result = await api.cleanup();
    toast(
      `Cleanup: ${plural(result.clusters_pruned, 'cluster')} pruned · ${plural(result.clusters_removed, 'cluster')} removed · ${plural(result.links_repaired, 'link')} repaired · ${plural(result.findings, 'finding')}.`,
      'success',
    );
    await Promise.all([system.loadFindings(), sources.loadList(), system.loadStatus()]);
  } catch (error) {
    toast(`Cleanup failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function curateVocabulary(): Promise<void> {
  try {
    await api.curateVocabulary();
    toast('Curating the claim-topic vocabulary…', 'info');
  } catch (error) {
    toast(`Vocabulary curation failed: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function retopicClaims(): Promise<void> {
  try {
    await api.retopicClaims();
    toast('Re-deriving claim topics from the claim texts…', 'info');
  } catch (error) {
    toast(`Topic re-derivation failed: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function reorganizeClaims(): Promise<void> {
  try {
    const result = await api.reorganizeAtoms();
    toast(
      result.moved === 0
        ? 'Nothing to move — no claim is in the legacy folder layout.'
        : `Moved ${plural(result.moved, 'claim')} out of ${plural(result.folders_removed ?? 0, 'folder')}.`,
      'success',
    );
    await system.loadVault();
  } catch (error) {
    toast(`Reorganize failed: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function collapseDuplicateClaims(): Promise<void> {
  const duplicates = system.duplicates;
  if (duplicates && duplicates.groups > 0) {
    const confirmed = await confirm({
      title: `Collapse ${plural(duplicates.groups, 'duplicate group')}?`,
      message:
        'One copy of every duplicate claim is kept; the indistinguishable copies move to .trash/atoms/. Nothing is rewritten.',
      confirmLabel: 'Collapse duplicates',
    });
    if (!confirmed) return;
  }
  try {
    const result = await api.collapseAtoms();
    toast(
      result.collapsed === 0
        ? 'Nothing to collapse — every claim exists once.'
        : `Kept one copy in ${plural(result.groups, 'group')}, moved ${plural(result.collapsed, 'file')} to the trash.`,
      'success',
    );
    await system.loadVault();
  } catch (error) {
    toast(`Collapse failed: ${firstLine(humanError(error))}`, 'error');
  }
}
