import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine } from '../format';
import { navigate, router, wikiPath } from '../router.svelte';
import { wiki } from '../stores/wiki.svelte';
import { toast } from '../toast.svelte';

export async function saveWikiPage(
  id: string,
  input: { title: string; tags: string[]; body: string },
): Promise<boolean> {
  try {
    const page = await wiki.save(id, input);
    toast('Wiki page saved — its source is being reprocessed.', 'success');
    navigate(wikiPath(page.id));
    return true;
  } catch (error) {
    toast(`Could not save the page: ${firstLine(humanError(error))}`, 'error');
    return false;
  }
}

export async function deleteWikiPage(id: string): Promise<void> {
  const page = wiki.selected?.id === id ? wiki.selected : null;
  const confirmed = await confirm({
    title: `Delete “${page?.title || 'this page'}”?`,
    message:
      'The page and its source representation are removed. The source bytes go to the trash; the page file is deleted.',
    confirmLabel: 'Delete page',
    danger: true,
  });
  if (!confirmed) return;

  try {
    await wiki.remove(id);
    if (router.current.view === 'wiki' && router.current.id === id) navigate('#/wiki');
    toast('Wiki page deleted.', 'success');
  } catch (error) {
    toast(`Could not delete the page: ${firstLine(humanError(error))}`, 'error');
  }
}
