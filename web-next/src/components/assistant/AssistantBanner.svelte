<script lang="ts">
  import type { Command } from '../../lib/api/types';
  import { api } from '../../lib/api';
  import { t } from '../../lib/i18n/index.svelte';
  import { criteriaChips } from '../../lib/assistant';
  import { navigate } from '../../lib/router.svelte';
  import { assistant } from '../../lib/stores/assistant.svelte';
  import Icon from '../ui/Icon.svelte';

  interface Props {
    /** The record the address names. */
    id: string;
    /** The server could not read the record: it was pruned. */
    expired?: boolean;
    /** How many members the set has now (live). */
    total?: number;
    /** How many of its members no longer resolve (deleted since the command ran). */
    missing?: number;
    /** The lens the address belongs to, for the "clear" target. */
    lens?: string;
  }

  let { id, expired = false, total = 0, missing = 0, lens = 'sources' }: Props = $props();

  let cmd = $state<Command | null>(null);
  let loadedFor = '';

  $effect(() => {
    if (!id || expired || loadedFor === id) return;
    loadedFor = id;
    void api
      .command(id)
      .then((result) => (cmd = result))
      .catch(() => (cmd = null));
  });

  const chips = $derived(cmd ? criteriaChips(cmd.criteria) : []);
  // The reading is ALWAYS a text line under the banner, never pills above the rows: the list is
  // what the reader came for, and the reading is a footnote to it.
  const chipText = $derived(chips.map((chip) => chip.text).join(' · '));
  const mapped = $derived(cmd?.mapped ?? []);
  const shown = $derived(cmd?.items?.length ?? 0);
  const cut = $derived(cmd !== null && shown > 0 && shown < cmd.total);
  const windowed = $derived(cmd !== null && cmd.vault !== undefined && cmd.scanned !== undefined && cmd.vault > cmd.scanned);

  function clear(): void {
    // Clearing is not "remove one parameter": the named criteria in the address ARE the assistant's
    // reading, so clearing goes to the bare lens.
    const path = lens === 'attention' ? 'attention' : lens === 'claims' ? 'claims' : lens === 'wiki' ? 'wiki' : 'sources';
    navigate(`#/${path}`);
  }

  function again(): void {
    if (cmd) void assistant.run(cmd.text);
  }

  const lensLabel = $derived(t(`view.${lens}`));
</script>

<div class="assistant-banner" class:banner--expired={expired} role="status">
  <span class="banner-mark"><Icon name="assistant" size={13} /></span>
  <span class="banner-kicker">{t('assistant.banner')}</span>

  {#if expired}
    <span class="banner-text">{lensLabel}</span>
    <span class="banner-note">{t('assistant.expired')}</span>
    <span class="grow"></span>
    <button class="banner-action" onclick={clear}>{t('assistant.clear')}</button>
  {:else}
    {#if cmd}
      <span class="banner-text truncate" title={cmd.text}>„{cmd.text}“</span>
      {#if cmd.resolution}
        <span class="banner-route">{t(`assistant.resolution.${cmd.resolution}`)}</span>
      {/if}
    {/if}

    <span class="banner-count">
      {total}
      {#if missing > 0}<span class="banner-missing">· {t('assistant.missing', { count: missing })}</span>{/if}
      {#if cut}<span class="banner-missing">· {t('assistant.truncated', { shown, total: cmd?.total ?? 0 })}</span>{/if}
      {#if windowed}<span class="banner-missing">· {t('assistant.windowed', { scanned: cmd?.scanned ?? 0, vault: cmd?.vault ?? 0 })}</span>{/if}
    </span>

    <span class="grow"></span>

    <button class="banner-action" title={cmd ? t('assistant.again') : ''} onclick={again} disabled={!cmd}>
      <Icon name="refresh" size={12} /> {t('assistant.again')}
    </button>
    <button class="banner-close" title={t('assistant.clear')} onclick={clear}>
      <Icon name="close" size={11} />
    </button>
  {/if}
</div>

{#if mapped.length > 0}
  <div class="assistant-why assistant-why--mapped">
    {#each mapped as m (m.kind + m.name)}
      <span class="mapped-row">
        <span class="mapped-kind">{t(`assistant.mapped.${m.kind}`)}</span>
        <span class="mapped-name">{m.name}</span>
        <span class="mapped-count">{t(`assistant.mapped.unit.${m.kind}`, { count: m.found })}</span>
      </span>
    {/each}
    {#each cmd?.notes ?? [] as note (note)}
      <span class="why-note">{note}</span>
    {/each}
  </div>
{:else if chipText || cmd?.rationale || (cmd?.notes?.length ?? 0) > 0}
  <div class="assistant-why">
    {#if chipText}
      <span class="why-applied">{t('assistant.applied')}: {chipText}</span>
    {/if}
    {#if cmd?.rationale}<span class="why-text">{cmd.rationale}</span>{/if}
    {#each cmd?.notes ?? [] as note (note)}
      <span class="why-note">{note}</span>
    {/each}
  </div>
{/if}

<style>
  .assistant-banner {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
    margin: 0 0 10px;
    padding: 7px 9px;
    border: 1px solid var(--accent-soft);
    border-radius: var(--radius-sm);
    background: var(--accent-soft);
    font-size: 11.5px;
    line-height: 1.45;
  }

  .banner--expired {
    border-color: var(--line);
    background: var(--bg-muted);
  }

  .banner-mark {
    display: flex;
    flex: none;
    color: var(--accent);
  }

  .banner--expired .banner-mark {
    color: var(--muted);
  }

  .banner-kicker {
    flex: none;
    color: var(--accent);
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .banner--expired .banner-kicker {
    color: var(--muted);
  }

  .banner-text {
    min-width: 0;
    max-width: 46ch;
    color: var(--ink-2);
    font-weight: 550;
  }

  .banner-note {
    color: var(--muted);
  }


  .banner-route {
    flex: none;
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--bg-elevated);
    color: var(--faint);
    font-size: 10px;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .assistant-why {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 10px;
    margin: -6px 0 10px;
    padding: 0 2px;
    font-size: 11.5px;
    line-height: 1.5;
  }

  .assistant-why--mapped {
    gap: 2px 16px;
  }

  .mapped-row {
    display: inline-flex;
    align-items: baseline;
    gap: 6px;
    white-space: nowrap;
  }

  .mapped-kind {
    color: var(--faint);
    font-size: 10px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .mapped-name {
    color: var(--ink-2);
    font-weight: 550;
  }

  .mapped-count {
    color: var(--faint);
    font-variant-numeric: tabular-nums;
  }

  .why-applied {
    color: var(--muted);
  }

  .why-text {
    color: var(--ink-2);
  }

  .why-note {
    color: var(--faint);
  }


  .banner-count {
    color: var(--muted);
    font-variant-numeric: tabular-nums;
  }

  .banner-missing {
    color: var(--faint);
  }

  .grow {
    flex: 1;
  }

  .banner-action {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 6px;
    border: 0;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--muted);
    font-size: 11px;
  }

  .banner-action:hover:not(:disabled) {
    background: var(--bg-hover);
    color: var(--ink);
  }

  .banner-action:disabled {
    opacity: 0.45;
  }

  .banner-close {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: var(--faint);
  }

  .banner-close:hover {
    background: var(--bg-hover);
    color: var(--ink);
  }
</style>
