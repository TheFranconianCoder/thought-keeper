import { humanError } from '../api/errors';
import { confirm } from '../confirm.svelte';
import { firstLine } from '../format';
import { navigate, router } from '../router.svelte';
import { asks } from '../stores/asks.svelte';
import { toast } from '../toast.svelte';

export function askQuestion(question: string): void {
  void asks.submit(question);
}

export async function deleteSavedAsk(id: string): Promise<void> {
  const saved = asks.items.find((item) => item.id === id) ?? asks.selected;
  const confirmed = await confirm({
    title: 'Delete this saved query?',
    message: `“${saved?.question ?? 'This question'}” and its answer move to .trash/asks/.`,
    confirmLabel: 'Delete query',
    danger: true,
  });
  if (!confirmed) return;

  try {
    await asks.remove(id);
    if (router.current.view === 'ask' && router.current.id === id) navigate('#/ask');
    toast('Saved query deleted.', 'success');
  } catch (error) {
    toast(`Could not delete the ask: ${firstLine(humanError(error))}`, 'error');
  }
}
