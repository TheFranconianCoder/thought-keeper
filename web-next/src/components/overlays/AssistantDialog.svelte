<script lang="ts">
  import { tick } from 'svelte';
  import { relTime } from '../../lib/format';
  import { t } from '../../lib/i18n/index.svelte';
  import { clearable } from '../../lib/clearable';
  import { closeAssistant, assistantOpen } from '../../lib/overlays';
  import { router } from '../../lib/router.svelte';
  import { assistant } from '../../lib/stores/assistant.svelte';
  import { criteriaChips } from '../../lib/assistant';
  import Icon from '../ui/Icon.svelte';

  // The field is ONE request at a time, on purpose: the answer is a lens, not a conversation. What it
  // produces is a record; the record hands out an address, and this window follows it like any other.
  const open = $derived(assistantOpen());

  // What "this" means right now. The server resolves the id to a source or a claim; here it only
  // has to be named, so the reader knows the field can point at it.
  const contextView = $derived(
    router.current.id && (router.current.view === 'claims' || router.current.view === 'source')
      ? router.current.view
      : '',
  );
  const suggestion = $derived(
    contextView === 'claims' ? t('assistant.suggest.sourcesOfClaim') : t('assistant.suggest.claimsOfSource'),
  );
  let input: HTMLInputElement | undefined = $state();
  let text = $state('');

  $effect(() => {
    if (!open) return;
    void tick().then(() => {
      input?.focus();
      input?.select();
    });
    const onKey = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        event.preventDefault();
        close();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  $effect(() => {
    if (open) void assistant.loadHistory();
  });

  function close(): void {
    closeAssistant();
    assistant.error = '';
  }

  async function submit(): Promise<void> {
    const value = text.trim();
    if (!value || assistant.busy) return;
    // The field is a command line, not a chat: it closes on submit. A research request can take a
    // while, and the top bar is where "working" belongs — this overlay would only be in the way.
    text = '';
    closeAssistant();
    void assistant.run(value);
  }

  function onKeydown(event: KeyboardEvent): void {
    if (event.key === 'Enter') {
      event.preventDefault();
      void submit();
    }
  }

  function recall(utterance: string): void {
    text = utterance;
    input?.focus();
  }
</script>

{#if open}
  <div class="assistant-overlay">
    <button class="assistant-bg" aria-label={t('common.close')} onclick={close}></button>
    <div class="assistant" role="dialog" aria-modal="true" aria-label={t('assistant.aria')}>
      <div class="assistant-head">
        <span class="mark" class:mark--busy={assistant.busy}>
          <Icon name="assistant" size={16} />
        </span>
        <div class="head-text">
          <div class="head-title">{t('assistant.title')}</div>
          <p class="head-sub">{t('assistant.sub')}</p>
        </div>
        <button class="icon-btn" title={t('common.close')} onclick={close}>
          <Icon name="close" size={14} />
        </button>
      </div>

      <div class="assistant-row">
        <input
          use:clearable
          bind:this={input}
          bind:value={text}
          class="assistant-input"
          placeholder={t('assistant.placeholder')}
          autocomplete="off"
          spellcheck="false"
          aria-label={t('assistant.aria')}
          onkeydown={onKeydown}
        />
        <button
          class="btn btn--primary"
          type="button"
          disabled={!text.trim() || assistant.busy}
          onclick={() => void submit()}
        >
          {assistant.busy ? t('assistant.working') : t('assistant.submit')}
        </button>
      </div>

      {#if assistant.error}
        <p class="assistant-error">{assistant.error}</p>
      {:else if contextView}
        <div class="assistant-context">
          <span class="context-label">
            {contextView === 'claims' ? t('assistant.context.claim') : t('assistant.context.source')}
          </span>
          <button class="context-suggestion" type="button" onclick={() => recall(suggestion)}>
            {suggestion}
          </button>
        </div>
      {:else}
        <p class="assistant-hint">{t('assistant.hint')}</p>
      {/if}

      {#if assistant.history.length > 0}
        <div class="assistant-history">
          <div class="history-label">
            {t('assistant.history')}
            <span class="history-total">{assistant.history.length}</span>
          </div>
          {#each assistant.history as cmd (cmd.id)}
            <button class="history-row" onclick={() => { assistant.reapply(cmd); text = ''; }}>
              <Icon name="clock" size={13} />
              <span class="history-main">
                <span class="history-text truncate">{cmd.text}</span>
                <span class="history-meta">
                  {relTime(cmd.created_at)}
                  {#if cmd.resolution === 'mapped' || cmd.resolution === 'research'}· {t('assistant.resolution.mapped')}{/if}
                  {#each criteriaChips(cmd.criteria).slice(0, 2) as chip (chip.key)}
                    · {chip.text}
                  {/each}
                </span>
              </span>
              <span class="history-count">{cmd.total}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .assistant-overlay {
    position: fixed;
    inset: 0;
    z-index: 70;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: min(12vh, 110px) 16px 16px;
  }

  .assistant-bg {
    position: absolute;
    inset: 0;
    border: 0;
    background: rgb(0 0 0 / 32%);
    backdrop-filter: blur(3px);
  }

  .assistant {
    position: relative;
    z-index: 1;
    width: min(620px, 100%);
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    animation: assistant-in 0.16s var(--ease);
  }

  .assistant-head {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 14px 14px 10px;
  }

  .mark {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    flex: none;
    border-radius: 8px;
    background: var(--accent-soft);
    color: var(--accent);
  }

  .mark--busy :global(.ico) {
    animation: spark-pulse 1.1s var(--ease) infinite;
  }

  .head-text {
    flex: 1;
    min-width: 0;
  }

  .head-title {
    color: var(--ink);
    font-size: 13.5px;
    font-weight: 600;
  }

  .head-sub {
    margin: 2px 0 0;
    color: var(--faint);
    font-size: 11.5px;
    line-height: 1.5;
  }

  .assistant-row {
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 0 14px 10px;
  }

  .assistant-input {
    flex: 1;
    min-width: 0;
    height: 38px;
    padding: 0 11px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg);
    color: var(--ink);
    font-size: 14px;
    outline: none;
  }

  .assistant-input:focus {
    border-color: var(--accent);
  }

  .assistant-hint,
  .assistant-error {
    margin: 0;
    padding: 0 14px 12px;
    color: var(--faint);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .assistant-error {
    color: var(--danger);
  }

  .assistant-context {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    padding: 0 14px 12px;
    font-size: 11.5px;
  }

  .context-label {
    color: var(--accent);
    font-weight: 550;
  }

  .context-suggestion {
    padding: 2px 7px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--bg-elevated);
    color: var(--muted);
    font-size: 11px;
  }

  .context-suggestion:hover {
    border-color: var(--accent);
    color: var(--ink);
  }

  .assistant-history {
    /* The whole recent history is listed (it expires after a week anyway), so the list scrolls
       inside the dialog instead of growing it past the screen. */
    max-height: min(44vh, 400px);
    overflow-y: auto;
    border-top: 1px solid var(--line);
    padding: 8px 6px 10px;
  }

  .history-total {
    margin-left: 6px;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .history-label {
    /* NOT sticky: a header that stays put covers the rows as they scroll under it, and the list is
       the thing the reader is looking at. It scrolls away with everything else. */
    padding: 2px 8px 6px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .history-row {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 8px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: inherit;
    text-align: left;
  }

  .history-row:hover {
    background: var(--bg-hover);
  }

  .history-row :global(.ico) {
    flex: none;
    color: var(--muted);
  }

  .history-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .history-text {
    color: var(--ink-2);
    font-size: 12.5px;
  }

  .history-meta {
    overflow: hidden;
    color: var(--faint);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .history-count {
    flex: none;
    color: var(--muted);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
  }

  @keyframes assistant-in {
    from {
      opacity: 0;
      transform: translateY(-5px) scale(0.995);
    }
  }

  @keyframes spark-pulse {
    0%,
    100% {
      opacity: 1;
      transform: scale(1);
    }
    50% {
      opacity: 0.45;
      transform: scale(0.86);
    }
  }
</style>
