import { api } from '../api';
import { ApiError, humanError } from '../api/errors';
import type { Command, CommandContext } from '../api/types';
import { navigate, router } from '../router.svelte';
import { t } from '../i18n/index.svelte';
import { toast } from '../toast.svelte';
import { asks } from './asks.svelte';

/**
 * How many commands the field lists. The records are pruned after a week anyway, so the whole
 * recent history fits — and the server caps a page at 200.
 */
const HISTORY_SIZE = 200;

/**
 * The assistant is a COMMAND line, not a chat: one request, one GUI state. The record it produces is
 * a URL, and the UI's only job is to go there.
 *
 * Two ways in, one way out:
 *  - the field in this window POSTs a command and receives the record;
 *  - ANY window (or the CLI) can publish one, and every open UI follows it over SSE.
 *
 * Both paths end in apply(), which navigates once per command id — so a response and the frame it
 * caused cannot navigate twice.
 */
class AssistantStore {
  /** A command is in flight somewhere: the topbar button shows it. */
  busy = $state(false);
  /** The utterance that is being read, for the field's own status line. */
  pending = $state('');
  /** The last failure, shown in the field. */
  error = $state('');

  /** The newest commands, for the Start lens. */
  history = $state<Command[]>([]);
  historyLoaded = $state(false);
  private historySeq = 0;

  /** The id of the last command this window followed. */
  lastID = $state('');

  private applied = '';
  private inFlight = 0;

  /** run submits one utterance and follows whatever it produces. */
  async run(text: string): Promise<Command | null> {
    const value = text.trim();
    if (!value) return null;
    this.error = '';
    this.begin(value);
    try {
      const cmd = await api.runCommand(value, currentContext());
      this.apply(cmd);
      return cmd;
    } catch (error) {
      // "I did not understand that" is a READING, not a failure of the app: it gets the parser's
      // own examples instead of the server's sentence.
      if (error instanceof ApiError && error.code === 'unparsed') {
        this.fail(t('assistant.unparsed'));
      } else {
        this.fail(t('assistant.failed', { error: humanError(error) }));
      }
      // The field closes on submit, so a failure has to reach the reader where they are.
      toast(this.error, 'error');
      return null;
    } finally {
      this.end();
    }
  }

  /** begin marks a command in flight. A counter, because two requests may overlap. */
  begin(text: string): void {
    this.inFlight++;
    this.busy = true;
    this.pending = text;
  }

  end(): void {
    this.inFlight = Math.max(0, this.inFlight - 1);
    if (this.inFlight === 0) {
      this.busy = false;
      this.pending = '';
    }
  }

  fail(message: string): void {
    this.error = message;
  }

  /**
   * apply follows one command. It navigates ONCE per id, which makes the response and the SSE frame
   * that follows it interchangeable — and it means a replayed frame (a reconnect) cannot move the
   * reader a second time.
   */
  apply(cmd: Command): void {
    if (!cmd.id || this.applied === cmd.id) return;
    this.applied = cmd.id;
    this.lastID = cmd.id;
    this.error = '';
    this.remember(cmd);

    if (cmd.lens === 'ask') {
      // An explicit question: the assistant delegates to Ask, exactly like the composer would. The
      // question is never written into the URL — that is why the record exists.
      const question = (cmd.criteria?.text ?? cmd.text).trim();
      void asks.submit(question, '');
      return;
    }
    if (cmd.url) navigate(cmd.url);
  }

  /** applyFrame is the SSE path: any client may send the command. */
  applyFrame(data: Record<string, unknown>): void {
    const status = String(data.status ?? '');
    if (status === 'running') {
      this.begin(String(data.text ?? ''));
      return;
    }
    if (status === 'failed') {
      this.end();
      this.fail(String(data.error ?? t('assistant.failedUnknown')));
      return;
    }
    if (status === 'pruned') {
      // The history moved under a reader who has an old address open. The Start lens re-reads; the
      // open list keeps its rows and the banner says the record is gone on its next read.
      return;
    }
    if (status !== 'done') return;
    this.end();
    const id = String(data.id ?? '');
    const url = String(data.url ?? '');
    if (!id || !url) return;
    this.apply({
      id,
      text: String(data.text ?? ''),
      lens: String(data.lens ?? ''),
      url,
      total: Number(data.total ?? 0),
      created_at: '',
      criteria: {},
    });
  }

  /** loadHistory reads the newest commands once (Start lens). */
  async loadHistory(): Promise<void> {
    const seq = ++this.historySeq;
    try {
      const result = await api.commands(HISTORY_SIZE);
      if (seq !== this.historySeq) return;
      this.history = result.commands ?? [];
      this.historyLoaded = true;
    } catch {
      if (seq === this.historySeq) this.historyLoaded = true;
    }
  }

  /** reapply follows a command from the history: the same address, the same set. */
  reapply(cmd: Command): void {
    this.applied = cmd.id;
    this.lastID = cmd.id;
    if (cmd.lens === 'ask') {
      const question = (cmd.criteria?.text ?? cmd.text).trim();
      void asks.submit(question, '');
      return;
    }
    if (cmd.url) navigate(cmd.url);
  }

  async remove(id: string): Promise<void> {
    await api.deleteCommand(id);
    this.history = this.history.filter((cmd) => cmd.id !== id);
  }

  private remember(cmd: Command): void {
    // Only a record the server wrote has a date: a frame from another window carries the address but
    // not the record, and the Start lens reads the real history when it opens.
    if (!cmd.created_at) return;
    this.history = [cmd, ...this.history.filter((item) => item.id !== cmd.id)].slice(0, HISTORY_SIZE);
  }
}

/**
 * currentContext is what the reader is looking at, so "this claim" and "this source" have a meaning.
 * Only the two detail lenses carry an id the server can resolve; everything else says nothing.
 */
function currentContext(): CommandContext | undefined {
  const route = router.current;
  if (!route.id) return undefined;
  if (route.view === 'source') return { view: 'source', id: route.id };
  if (route.view === 'claims') return { view: 'claims', id: route.id };
  return undefined;
}

export const assistant = new AssistantStore();
