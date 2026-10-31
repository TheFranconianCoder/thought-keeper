import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine } from '../format';
import { t } from '../i18n/index.svelte';
import { navigate, router, wikiPath } from '../router.svelte';
import { wiki } from '../stores/wiki.svelte';
import { toast } from '../toast.svelte';

export async function saveWikiPage(
  id: string,
  input: { title: string; tags: string[]; body: string },
): Promise<boolean> {
  try {
    const page = await wiki.save(id, input);
    toast(t('wiki.saved'), 'success');
    navigate(wikiPath(page.id));
    return true;
  } catch (error) {
    toast(t('wiki.saveFailed', { error: firstLine(humanError(error)) }), 'error');
    return false;
  }
}

export async function deleteWikiPage(id: string): Promise<void> {
  const page = wiki.selected?.id === id ? wiki.selected : null;
  const confirmed = await confirm({
    title: t('wiki.deleteTitle', { page: page?.title || t('wiki.thisPage') }),
    message: t('wiki.deleteMessage'),
    confirmLabel: t('wiki.deleteConfirm'),
    danger: true,
  });
  if (!confirmed) return;

  try {
    await wiki.remove(id);
    if (router.current.view === 'wiki' && router.current.id === id) navigate('#/wiki');
    toast(t('wiki.deleted'), 'success');
  } catch (error) {
    toast(t('wiki.deleteFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}

/** toggleWikiFavorite writes the reader's star on a page. The page's frontmatter is the truth; the
 *  list and the open detail are updated in place so the star answers at once. */
export async function toggleWikiFavorite(id: string, on: boolean): Promise<void> {
  try {
    const page = await wiki.toggleFavorite(id, on);
    if (wiki.selectedId === id) wiki.selected = page;
  } catch (error) {
    toast(t('wiki.favoriteFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}
