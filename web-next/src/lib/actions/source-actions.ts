import { api } from '../api';
import { ApiError, humanError } from '../api/errors';
import type { Source } from '../api/types';
import { confirm } from '../confirm.svelte';
import { firstLine, isURL, plural } from '../format';
import { navigate, router, sourcePath } from '../router.svelte';
import { sources } from '../stores/sources.svelte';
import { system } from '../stores/system.svelte';
import { toast } from '../toast.svelte';
import { waitForRun } from './runs';

function labelOf(source: Source | undefined, hash: string): string {
  return source?.title || source?.original_name || hash.slice(0, 12);
}

async function refreshSourceViews(hash?: string): Promise<void> {
  await Promise.all([sources.loadList(), system.loadStatus()]);
  if (hash && sources.selectedHash === hash) await sources.refresh(hash);
}

export async function addPathOrURL(value: string, bookmark = false): Promise<void> {
  const input = value.trim();
  if (!input) return;
  system.notice = isURL(input) ? 'Fetching the link…' : 'Adding the source…';
  try {
    const source = isURL(input) ? await api.addURL(input, bookmark) : await api.addPath(input);
    toast(`Added ${source.kind || 'source'} · ${source.title || source.hash.slice(0, 12)}`, 'success');
    system.notice = '';
    await refreshSourceViews();
  } catch (error) {
    system.notice = `Could not add the source: ${firstLine(humanError(error))}`;
    throw error;
  }
}

export async function addManyURLs(lines: string[]): Promise<number> {
  const urls = lines.map((line) => line.trim()).filter(isURL);
  if (urls.length === 0) {
    toast('No http(s) URLs found in the input.', 'warning');
    return 0;
  }
  try {
    const result = await api.addURLs(urls);
    toast(`Queued ${plural(result.accepted, 'URL')} — sources appear as they are fetched.`, 'success');
    await refreshSourceViews();
    return result.accepted;
  } catch (error) {
    toast(`URL import failed: ${firstLine(humanError(error))}`, 'error');
    throw error;
  }
}

export async function uploadFiles(files: FileList | File[]): Promise<void> {
  const list = Array.from(files);
  if (list.length === 0) return;
  let uploaded = 0;
  for (const file of list) {
    system.notice = `Uploading ${file.name}…`;
    try {
      await api.addUpload(file);
      uploaded++;
    } catch (error) {
      system.notice = '';
      toast(`Upload failed: ${firstLine(humanError(error))}`, 'error');
      if (uploaded > 0) await refreshSourceViews();
      return;
    }
  }
  system.notice = '';
  toast(`Uploaded ${plural(uploaded, 'file')}.`, 'success');
  await refreshSourceViews();
}

// attachSnapshot gives a blocked URL source the bytes the app could not fetch. The human is already
// logged in in their browser; saving the page and attaching it is the whole exchange, and the URL
// stays the source's address.
export async function attachSnapshot(hash: string, file: File): Promise<void> {
  system.notice = `Attaching ${file.name}…`;
  try {
    const source = await api.attachSnapshot(hash, file);
    toast(`Attached ${file.name} · ${source.title || 'the page'}`, 'success');
    system.notice = '';
    await refreshSourceViews(hash);
  } catch (error) {
    system.notice = '';
    toast(`Could not attach the snapshot: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function deleteSource(hash: string): Promise<void> {
  const source = sources.find(hash);
  const label = labelOf(source, hash);
  const ownsChildren = source?.rss === true || source?.sitemap === true;
  const confirmed = await confirm({
    title: `Delete “${label}”?`,
    message: ownsChildren
      ? 'Its claims, their provenance and every page it owns go too. The snapshot and its derived files move to the trash.'
      : 'Its claims and their provenance go too. The snapshot and its derived files move to the trash.',
    confirmLabel: ownsChildren ? 'Delete and its pages' : 'Delete source',
    danger: true,
  });
  if (!confirmed) return;

  system.notice = `Deleting ${label}…`;
  // Back to the list NOW: a delete waits for running document/cluster work on the server, and the
  // reader has already decided. The row leaves optimistically; a failure reloads the list.
  const leaving = router.current.view === 'source' && router.current.id === hash;
  sources.drop(hash);
  if (leaving) {
    sources.clearSelection();
    navigate('#/sources');
  }
  try {
    const result = await api.deleteSource(hash);
    const dependents = result.dependents_removed ?? 0;
    toast(
      dependents > 0
        ? `Deleted ${label} and ${plural(dependents, 'dependent source')}.`
        : `Deleted ${label}.`,
      'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(`Could not delete the source: ${firstLine(humanError(error))}`, 'error');
    // The server did not delete it: the optimistic removal is undone by reloading the list.
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
      toast('Capturing — the outline follows.');
    } else {
      toast(result.detail || 'Nothing to capture.');
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(`Capture failed: ${firstLine(humanError(error))}`, 'error');
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
      toast(`Refresh failed: ${result.detail || 'the site did not answer with the source'}`, 'error');
    } else if (result.attached) {
      toast('This source has a human-supplied snapshot; it is never fetched.', 'info');
    } else if (result.blocked) {
      toast(`Refresh blocked: ${result.detail || 'the site asked for a login or a subscription'}`, 'info');
    } else if (result.changed) {
      toast('Refreshed — the source changed.', 'success');
    } else if (result.discovered) {
      toast(`Refreshed — ${plural(result.discovered, 'new page')} queued.`, 'success');
    } else if (result.removed) {
      toast(`Refreshed — ${plural(result.removed, 'page')} removed.`, 'success');
    } else {
      toast('Refreshed — nothing changed.', 'success');
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(`Refresh failed: ${firstLine(humanError(error))}`, 'error');
    await refreshSourceViews(hash);
  } finally {
    system.clearBanner();
  }
}

export async function distillSource(hash: string, reset = false): Promise<void> {  const source = sources.find(hash);
  const label = labelOf(source, hash);

  if (!reset && source && source.atom_count > 0) {
    const partsLeft = Math.max(0, (source.parts_total ?? 0) - (source.parts_done ?? 0));
    if (partsLeft === 0 && (source.atoms_left ?? 0) === 0) {
      const again = await confirm({
        title: `Nothing left to continue for “${label}”.`,
        message: 'Everything is already derived. Derive the source again from scratch? Its claims are replaced.',
        confirmLabel: 'Derive again',
        danger: true,
      });
      if (again) await distillSource(hash, true);
      return;
    }
  }

  if (reset) {
    const confirmed = await confirm({
      title: `Derive “${label}” again from scratch?`,
      message: 'The outline and the claims of this source are replaced and derived again.',
      confirmLabel: 'Derive again',
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
      toast('Already running — the source is being derived.', 'info');
    } else {
      toast(`Distill failed: ${firstLine(humanError(error))}`, 'error');
    }
    await refreshSourceViews(hash);
  }
}

/** refetchSource tries the URL again for an attached snapshot. If the page is readable, the fresh
 *  snapshot replaces the attachment and the claims are rebuilt; a wall or a failure keeps it. */
export async function refetchSource(hash: string): Promise<void> {
  const source = sources.find(hash);
  const label = labelOf(source, hash);
  const attachedFrom = source?.attached_from ? ` (${source.attached_from})` : '';
  const confirmed = await confirm({
    title: `Try “${label}” again?`,
    message: `The URL is fetched once. If the page is readable, its snapshot replaces the attached file${attachedFrom} and the claims are rebuilt when its text changed. If the page is still blocked or the fetch fails, the attached snapshot stays exactly as it is.`,
    confirmLabel: 'Try the URL again',
  });
  if (!confirmed) return;

  system.notice = 'Fetching the URL again…';
  try {
    const result = await api.refetchSource(hash);
    if (result.replaced) {
      toast('The page was readable — the attached snapshot was replaced.', 'success');
    } else if (result.blocked) {
      toast(
        `Still blocked${result.detail ? ` (${result.detail})` : ''} — the attached snapshot is unchanged.`,
        'warning',
      );
    } else if (result.failed) {
      toast(
        `The fetch failed — the attached snapshot is unchanged${result.detail ? `: ${firstLine(result.detail)}` : ''}.`,
        'error',
      );
    } else {
      toast('The URL answered with no new content; nothing was replaced.', 'info');
    }
    await refreshSourceViews(hash);
  } catch (error) {
    toast(`Refetch failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function resetSource(hash: string): Promise<void> {
  const source = sources.find(hash);
  const label = labelOf(source, hash);
  const confirmed = await confirm({
    title: `Reset “${label}”?`,
    message:
      'The outline and the claims of this source are thrown away and built again from the stored file. The source itself stays.',
    confirmLabel: 'Reset derivation',
    danger: true,
  });
  if (!confirmed) return;

  system.markBanner('reset');
  sources.markRunning(hash, 'capture');
  try {
    const result = await api.resetSource(hash);
    toast(`Reset: ${plural(result.nodes, 'node')}, ${plural(result.atoms, 'claim')} removed.`, 'success');
    await refreshSourceViews(hash);
  } catch (error) {
    system.clearBanner();
    toast(`Reset failed: ${firstLine(humanError(error))}`, 'error');
    await refreshSourceViews(hash);
  }
}

export async function rebuildSourceTree(hash: string): Promise<void> {
  system.markBanner('outline');
  try {
    const result = await api.rebuildTree(hash);
    toast(
      `Outline rebuilt: ${plural(result.nodes, 'node')}${result.pages ? `, ${plural(result.pages, 'page')}` : ''}${result.ocr ? ' · OCR' : ''}.`,
      'success',
    );
    await refreshSourceViews(hash);
  } catch (error) {
    toast(`Outline rebuild failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.clearBanner();
  }
}

export async function refreshURLs(): Promise<void> {
  system.notice = 'Refreshing URL sources…';
  try {
    const result = await api.refreshURLs();
    const notes = [
      result.blocked ? `${result.blocked} blocked` : '',
      result.attached ? `${result.attached} attached, not fetched` : '',
      result.discovered ? `${result.discovered} new page${result.discovered === 1 ? '' : 's'}` : '',
      result.removed ? `${result.removed} removed` : '',
    ].filter(Boolean);
    toast(
      `URLs checked ${result.checked} · changed ${result.changed} · failed ${result.failed}${notes.length ? ` · ${notes.join(' · ')}` : ''}.`,
      result.failed > 0 ? 'warning' : 'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(`URL refresh failed: ${firstLine(humanError(error))}`, 'error');
  } finally {
    system.notice = '';
  }
}

export async function fixURLs(): Promise<void> {
  try {
    const result = await api.fixURLs();
    toast(
      result.fixed > 0
        ? `Repaired ${result.fixed} of ${result.checked} source(s) — they are URL sources again.`
        : `Nothing to repair: ${result.checked} Markdown source(s) checked.`,
      'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(`URL repair failed: ${firstLine(humanError(error))}`, 'error');
  }
}

export async function migrateURLAddresses(): Promise<void> {
  try {
    const result = await api.migrateURLAddresses();
    toast(
      result.moved === 0
        ? 'Every URL source already has its URL address.'
        : `Moved ${plural(result.moved, 'URL source')} · ${plural(result.atoms_repointed, 'claim')} re-pointed.`,
      'success',
    );
    await refreshSourceViews();
  } catch (error) {
    toast(`URL address migration failed: ${firstLine(humanError(error))}`, 'error');
  }
}

/** openSourceFromHash is the one way a hash becomes a source page. */
export function openSourceFromHash(hash: string, node?: string): void {
  navigate(sourcePath(hash, node ? { tab: 'snapshot', part: node } : {}));
}
