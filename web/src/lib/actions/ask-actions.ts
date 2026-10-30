import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine } from '../format';
import { t } from '../i18n/index.svelte';
import { navigate, router } from '../router.svelte';
import { asks } from '../stores/asks.svelte';
import { toast } from '../toast.svelte';

export function askQuestion(question: string): void {
  void asks.submit(question);
}

/** askAgain re-asks a saved question. The old answer stays; the new one is a new saved query. A
 *  question that was scoped to a source is asked against that source again. */
export function askAgain(question: string, source = ''): void {
  void asks.submit(question, source);
}

export async function deleteSavedAsk(id: string): Promise<void> {
  const saved = asks.items.find((item) => item.id === id) ?? asks.selected;
  const confirmed = await confirm({
    title: t('ask.deleteTitle'),
    message: t('ask.deleteMessage', { question: saved?.question ?? '' }),
    confirmLabel: t('ask.deleteConfirm'),
    danger: true,
  });
  if (!confirmed) return;

  try {
    await asks.remove(id);
    if (router.current.view === 'ask' && router.current.id === id) navigate('#/ask');
    toast(t('ask.deleted'), 'success');
  } catch (error) {
    toast(t('ask.deleteFailed', { error: firstLine(humanError(error)) }), 'error');
  }
}
