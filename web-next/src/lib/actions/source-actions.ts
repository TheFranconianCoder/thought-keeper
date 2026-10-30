import { api } from '../api';
import { ApiError, humanError } from '../api/errors';
import type { Source } from '../api/types';
import { confirm } from '../confirm.svelte';
import { firstLine, isURL } from '../format';
import { t, tn } from '../i18n/index.svelte';
import { navigate, router, sourcePath } from '../router.svelte';
import { sources } from '../stores/sources.svelte';
import { bookmarkWindow, failedWindow, sourceWindow } from '../stores/source-window.svelte';
import { system } from '../stores/system.svelte';
import { toast } from '../toast.svelte';
import { waitForRun } from './runs';

function labelOf(source: Source | undefined, hash: string): string {
  return source?.title || source?.original_name || hash.slice(0, 12);
}

/** findSource looks in the visible windows first, then in the full list other code may keep. */
function findSource(hash: string): Source | undefined {
  if (sources.selected?.hash === hash) return sources.selected;
  return (
    sourceWindow.find(hash) ??
    bookmarkWindow.find(hash) ??
    failedWindow.find(hash) ??
    sources.find(hash)
  );
}

async function refreshSourceViews(hash?: string): Promise<void> {
  // Every source list is a window now: invalidate the caches and let each mounted lens read its
  // visible range again.
  sourceWindow.invalidate();
  bookmarkWindow.invalidate();
  failedWindow.invalidate();
  await system.loadStatus();
  if (hash && sources.selectedHash === hash) await sources.refresh(hash);
}

export async function addPathOrURL(value: string, bookmark = false): Promise<void> {
  const input = value.trim();
  if (!input) return;
  system.notice = isURL(input) ? t('sources.fetchingLink') : t('sources.addingSource');
  try {
    const source = isURL(input) ? await api.addURL(input, bookmark) : await api.addPath(input);
    toast(
      t('sources.added', {
        kind: source.kind || t('sources.wordSource'),
        title: source.title || source.hash.slice(0, 12),
      }),
      'success',
    );
    system.notice = '';
    await refreshSourceViews();
  } catch (error) {
    system.notice = t('sources.addFailed', { error: firstLine(humanError(error)) });
    throw error;
  }
}

export async function addManyURLs(lines: string[]): Promise<number> {
  const urls = lines.map((line) => line.trim()).filter(isURL);
  if (urls.length === 0) {
    toast(t('sources.noUrlsFound'), 'warning');
    return 0;
  }
  try {
    const result = await api.addURLs(urls);
    toast(tn('sources.queuedUrls', result.accepted), 'success');
    await refreshSourceViews();
    return result.accepted;
  } catch (error) {
    toast(t('sources.urlImportFailed', { error: firstLine(humanError(error)) }), 'error');
    throw error;
  }
}

export async function uploadFiles(files: FileList | File[]): Promise<void> {
  const list = Array.from(files);
  if (list.length === 0) return;
  let uploaded = 0;
  for (const file of list) {
    system.notice = t('sources.uploading', { name: file.name });
    try {
      await api.addUpload(file);
      uploaded++;
    } catch (error) {
      system.notice = '';
      toast(t('sources.uploadFailed', { error: firstLine(humanError(error)) }), 'error');
      if (uploaded > 0) await refreshSourceViews();
      return;
    }
  }
  system.notice = '';
  toast(tn('sources.uploaded', uploaded), 'success');
  await refreshSourceViews();
}

// attachSnapshot gives a blocked URL source the bytes the app could not fetch. The human is already
// logged in in their browser; saving the page and attaching it is the whole exchange, and the URL
// stays the source's address.
export async function attachSnapshot(hash: string, file: File): Promise<void> {
  system.notice = t('sources.attaching', { name: file.name });
  try {
    const source = await api.attachSnapshot(hash, file);
    toast(
      t('sources.attachedToast', {
        name: file.name,
        title: source.title || t('sources.thePage'),
      }),
      'success',
    );
    system.notice = '';
    await refreshSourceViews(hash);
  } catch (error) {
    system.notice = '';
    toast(t('sources.attachFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function deleteSource(hash: string): Promise<void> {
  const source = findSource(hash);
  const label = labelOf(source, hash);
  const ownsChildren = source?.rss === true || source?.sitemap === true;
  const confirmed = await confirm({
    title: t('sources.deleteTitle', { label }),
    message: ownsChildren ? t('sources.deleteMessagePages') : t('sources.deleteMessage'),
    confirmLabel: ownsChildren ? t('sources.deleteConfirmPages') : t('sources.deleteConfirm'),
    danger: true,
  });
  if (!confirmed) return;

  system.notice = t('sources.deleting', { label });
  // Back to the list NOW: a delete waits for running document/cluster work on the server, and the
  // reader has already decided. The store keeps the row hidden across the list reloads that source
  // events cause during that wait; only a failed delete brings it back.
  const leaving = router.current.view === 'source' && router.current.id === hash;
  sources.drop(hash);
  sourceWindow.drop(hash);
  bookmarkWindow.drop(hash);
  failedWindow.drop(hash);
  if (leaving) {
    sources.clearSelection();
    navigate('#/sources');
  }
  try {
    const result = await api.deleteSource(hash);
    const dependents = result.dependents_removed ?? 0;
    // Keep hiding it until the list has been read WITHOUT it: a read that started before the delete
    // committed may still carry the row.
    await refreshSourceViews();
    sources.finishDelete(hash);
    sourceWindow.reveal(hash);
    bookmarkWindow.reveal(hash);
    failedWindow.reveal(hash);
    toast(
      dependents > 0
        ? tn('sources.deletedWithDependents', dependents, { label })
        : t('sources.deleted', { label }),
      'success',
    );
  } catch (error) {
    toast(t('sources.deleteFailed', { error: firstLine(humanError(error)) }), 'error');
    // The server did not delete it: stop hiding it and let the reload bring the row back.
    sources.finishDelete(hash);
    sourceWindow.reveal(hash);
    bookmarkWindow.reveal(hash);
    failedWindow.reveal(hash);
    await refreshSourceViews();
  } finally {
    system.notice = '';
  }
}

export async function captureSource(hash: string): Promise<void> {
  try {
    const result = await api.capture(hash);
    if (result.queued) {
      sources.markRunning(hash, 'capture');
      system.markBanner('capture');
      toast(t('sources.capturing'));
    } else {
      toast(result.detail || t('sources.nothingToCapture'));
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(t('sources.captureFailed', { error: firstLine(humanError(error)) }), 'error');
    await refreshSourceViews(hash);
  }
}

// refreshSource fetches ONE source now: a feed or a sitemap re-expands its list, a page is
// re-fetched. It is the list's only action — there is no document to claim, outline or distil.
export async function refreshSource(hash: string): Promise<void> {
  system.markBanner('refresh');
  try {
    const result = await api.refreshSource(hash);
    if (result.failed > 0) {
      toast(
        t('sources.refreshFailed', { error: result.detail || t('sources.siteDidNotAnswer') }),
        'error',
      );
    } else if (result.attached) {
      toast(t('sources.attachedNeverFetched'), 'info');
    } else if (result.blocked) {
      toast(
        t('sources.refreshBlocked', { error: result.detail || t('sources.siteAskedLogin') }),
        'info',
      );
    } else if (result.changed) {
      toast(t('sources.refreshedChanged'), 'success');
    } else if (result.discovered) {
      toast(tn('sources.refreshedDiscovered', result.discovered), 'success');
    } else if (result.removed) {
      toast(tn('sources.refreshedRemoved', result.removed), 'success');
    } else {
      toast(t('sources.refreshedUnchanged'), 'success');
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(t('sources.refreshFailed', { error: firstLine(humanError(error)) }), 'error');
    await refreshSourceViews(hash);
  } finally {
    system.clearBanner();
  }
}

export async function distillSource(hash: string, reset = false): Promise<void> {
  const source = findSource(hash);
  const label = labelOf(source, hash);

  if (!reset && source && source.atom_count > 0) {
    const partsLeft = Math.max(0, (source.parts_total ?? 0) - (source.parts_done ?? 0));
    if (partsLeft === 0 && (source.atoms_left ?? 0) === 0) {
      const again = await confirm({
        title: t('sources.nothingLeft', { label }),
        message: t('sources.deriveAgainQuestion'),
        confirmLabel: t('sources.deriveAgain'),
        danger: true,
      });
      if (again) await distillSource(hash, true);
      return;
    }
  }

  if (reset) {
    const confirmed = await confirm({
      title: t('sources.deriveAgainTitle', { label }),
      message: t('sources.deriveAgainMessage'),
      confirmLabel: t('sources.deriveAgain'),
      danger: true,
    });
    if (!confirmed) return;
  }

  system.markBanner('distill');
  sources.markRunning(hash, 'distill');
  try {
    const result = await api.distill(hash, reset);
    // The stream carries the run; a short poll makes the outcome deterministic even if the stream
    // was reconnecting exactly when the run ended.
    void waitForRun(result.run_id, 10 * 60_000).then((run) => {
      if (!run || run.status !== 'running') {
        system.clearBanner();
        void refreshSourceViews(hash);
      }
    });
  } catch (error) {
    system.clearBanner();
    if (error instanceof ApiError && error.code === 'conflict') {
      toast(t('sources.alreadyRunning'), 'info');
    } else {
      toast(t('sources.distillFailed', { error: firstLine(humanError(error)) }), 'error');
    }
    await refreshSourceViews(hash);
  }
}

/** refetchSource tries the URL again for an attached snapshot. If the page is readable, the fresh
 *  snapshot replaces the attachment and the claims are rebuilt; a wall or a failure keeps it. */
export async function refetchSource(hash: string): Promise<void> {
  const source = findSource(hash);
  const label = labelOf(source, hash);
  const attachedFrom = source?.attached_from ? ` (${source.attached_from})` : '';
  const confirmed = await confirm({
    title: t('sources.refetchTitle', { label }),
    message: t('sources.refetchMessage', { from: attachedFrom }),
    confirmLabel: t('sources.refetchConfirm'),
  });
  if (!confirmed) return;

  system.notice = t('sources.refetching');
  try {
    const result = await api.refetchSource(hash);
    if (result.replaced) {
      toast(t('sources.refetchReplaced'), 'success');
    } else if (result.blocked) {
      toast(
        result.detail
          ? t('sources.refetchStillBlockedDetail', { error: result.detail })
          : t('sources.refetchStillBlocked'),
        'warning',
      );
    } else if (result.failed) {
      toast(
        result.detail
          ? t('sources.refetchFailedDetail', { error: firstLine(result.detail) })
          : t('sources.refetchFailed'),
        'error',
      );
    } else {
      toast(t('sources.refetchNoContent'), 'info');
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(t('sources.refetchError', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function resetSource(hash: string): Promise<void> {
  const source = findSource(hash);
  const label = labelOf(source, hash);
  const confirmed = await confirm({
    title: t('sources.resetTitle', { label }),
    message: t('sources.resetMessage'),
    confirmLabel: t('sources.resetConfirm'),
    danger: true,
  });
  if (!confirmed) return;

  system.markBanner('reset');
  sources.markRunning(hash, 'capture');
  try {
    const result = await api.resetSource(hash);
    toast(
      t('sources.resetDone', {
        parts: `${tn('sources.counts.nodes', result.nodes)}, ${tn('sources.counts.claims', result.atoms)}`,
      }),
      'success',
    );
    await refreshSourceViews(hash);
  } catch (error) {
    system.clearBanner();
    toast(t('sources.resetFailed', { error: firstLine(humanError(error)) }), 'error');
    await refreshSourceViews(hash);
  }
}

export async function rebuildSourceTree(hash: string): Promise<void> {
  system.markBanner('outline');
  try {
    const result = await api.rebuildTree(hash);
    const counts = [tn('sources.counts.nodes', result.nodes)];
    if (result.pages) counts.push(tn('sources.counts.pages', result.pages));
    toast(
      t('sources.outlineRebuilt', {
        parts: `${counts.join(', ')}${result.ocr ? ' · OCR' : ''}`,
      }),
      'success',
    );
    await refreshSourceViews(hash);
  } catch (error) {
    toast(t('sources.outlineFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.clearBanner();
  }
}

export async function refreshURLs(): Promise<void> {
  system.notice = t('sources.refreshingUrls');
  try {
    const result = await api.refreshURLs();
    const notes = [
      result.blocked ? t('sources.noteBlocked', { count: result.blocked }) : '',
      result.attached ? t('sources.noteAttached', { count: result.attached }) : '',
      result.discovered ? tn('sources.counts.newPages', result.discovered) : '',
      result.removed ? t('sources.noteRemoved', { count: result.removed }) : '',
    ].filter(Boolean);
    toast(
      t('sources.urlsChecked', {
        checked: result.checked,
        changed: result.changed,
        failed: result.failed,
        notes: notes.length ? ` · ${notes.join(' · ')}` : '',
      }),
      result.failed > 0 ? 'warning' : 'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(t('sources.urlRefreshFailed', { error: firstLine(humanError(error)) }), 'error');
  } finally {
    system.notice = '';
  }
}

export async function fixURLs(): Promise<void> {
  try {
    const result = await api.fixURLs();
    toast(
      result.fixed > 0
        ? t('sources.repaired', { fixed: result.fixed, checked: result.checked })
        : tn('sources.nothingToRepair', result.checked),
      'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(t('sources.urlRepairFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

export async function migrateURLAddresses(): Promise<void> {
  try {
    const result = await api.migrateURLAddresses();
    toast(
      result.moved === 0
        ? t('sources.migrateNothing')
        : t('sources.migrateDone', {
            moved: tn('sources.counts.urlSources', result.moved),
            claims: tn('sources.counts.claims', result.atoms_repointed),
          }),
      'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(t('sources.migrateFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

/** openSourceFromHash is the one way a hash becomes a source page. */
export function openSourceFromHash(hash: string, node?: string): void {
  navigate(sourcePath(hash, node ? { tab: 'snapshot', part: node } : {}));
}
