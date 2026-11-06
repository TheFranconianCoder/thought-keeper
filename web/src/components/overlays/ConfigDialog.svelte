<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { api } from '../../lib/api';
  import { humanError } from '../../lib/api/errors';
  import type { ConfigFieldView, ConfigView } from '../../lib/api/types';
  import { t } from '../../lib/i18n/index.svelte';
  import { closeConfig, configOpen } from '../../lib/overlays';
  import { router } from '../../lib/router.svelte';
  import { toast } from '../../lib/toast.svelte';
  import Icon from '../ui/Icon.svelte';

  // The configuration is a READ of the running process: what it is set to, where each value came
  // from, and whether the file on disk says something else. It is an overlay, not a lens — the
  // reader opens it from the gear, from a capability chip, or not at all.
  const open = $derived(configOpen());
  const target = $derived(router.current.params.get('field') ?? '');

  let view = $state<ConfigView | null>(null);
  let loading = $state(false);
  let error = $state('');
  let query = $state('');
  let panel: HTMLDivElement | undefined = $state();

  const shown = $derived.by(() => {
    const needles = query.trim().toLowerCase();
    const sections = view?.sections ?? [];
    if (!needles) return sections;
    return sections
      .map((section) => ({
        name: section.name,
        fields: section.fields.filter((field) => matches(field, needles)),
      }))
      .filter((section) => section.fields.length > 0);
  });

  // A translated name when there is one; empty otherwise. The KEY is always the stable name, so an
  // untranslated field shows its key once instead of repeating it as a name and a subline.
  function label(field: ConfigFieldView): string {
    const key = `config.field.${field.key}`;
    const text = t(key);
    return text === key ? '' : text;
  }

  // A new knob without a catalog entry still gets a text line instead of a bare key; the key is
  // always the subline, so the row never loses its YAML address.
  function humanize(key: string): string {
    const leaf = key.split('.').pop() ?? key;
    const text = leaf.replace(/_/g, ' ');
    return text.charAt(0).toUpperCase() + text.slice(1);
  }

  function name(field: ConfigFieldView): string {
    return label(field) || humanize(field.key);
  }

  function matches(field: ConfigFieldView, needle: string): boolean {
    return field.key.toLowerCase().includes(needle) || name(field).toLowerCase().includes(needle);
  }

  // A path or tool row reads the same everywhere: the short name on the first line, the absolute
  // path on the second. An absolute configured path is shortened to its base name; an empty
  // browser.path falls back to the engine name it searches for.
  function shortPath(value: string): string {
    const trimmed = value.replace(/[\\/]+$/, '');
    const parts = trimmed.split(/[\\/]/);
    return parts[parts.length - 1] || trimmed;
  }

  function valueText(field: ConfigFieldView): string {
    if (field.secret) {
      // A header list names its headers; their VALUES never travel. The key itself only says
      // whether it has one.
      if (Array.isArray(field.value)) {
        return field.value.length ? field.value.join(', ') : t('config.emptyValue');
      }
      return field.set ? t('config.secretSet') : t('config.secretUnset');
    }
    if (field.kind === 'path' || field.kind === 'tool') {
      const raw = typeof field.value === 'string' && field.value.trim()
        ? field.value.trim()
        : field.lookup ?? '';
      return raw ? shortPath(raw) : t('config.emptyValue');
    }
    const value = field.value;
    if (value === null || value === undefined || value === '') return t('config.emptyValue');
    if (typeof value === 'boolean') return value ? t('config.on') : t('config.off');
    if (Array.isArray(value)) return value.length ? value.join(', ') : t('config.emptyValue');
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
  }

  function sourceText(field: ConfigFieldView): string {
    switch (field.source) {
      case 'environment':
        return t('config.sourceEnvironment', { env: field.env ?? '' });
      case 'file':
        return t('config.sourceFile');
      default:
        return t('config.sourceDefault');
    }
  }

  async function load(): Promise<void> {
    loading = true;
    error = '';
    try {
      view = await api.config();
    } catch (err) {
      error = humanError(err);
    } finally {
      loading = false;
    }
  }

  async function copyPath(): Promise<void> {
    if (!view) return;
    try {
      await navigator.clipboard.writeText(view.path);
      toast(t('config.copied'), 'success');
    } catch {
      toast(t('config.copyFailed'), 'error');
    }
  }

  $effect(() => {
    if (!open) return;
    untrack(() => void load());
  });

  // A window that is opened FROM a knob (a Status capability chip) lands on it: the parameter is the
  // YAML key, a name, never free text.
  $effect(() => {
    if (!open || !view || !target) return;
    void tick().then(() =>
      document.getElementById(`config-${target}`)?.scrollIntoView({ block: 'center' }),
    );
  });

  $effect(() => {
    if (!open) return;
    void tick().then(() => panel?.focus());
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      closeConfig();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });
</script>

{#if open}
  <div class="config-overlay">
    <button class="config-bg" aria-label={t('common.close')} onclick={closeConfig}></button>
    <div
      class="config"
      role="dialog"
      aria-modal="true"
      aria-label={t('config.title')}
      tabindex="-1"
      bind:this={panel}
    >
      <div class="config-head">
        <span class="mark"><Icon name="settings" size={16} /></span>
        <div class="head-text">
          <div class="head-title">{t('config.title')}</div>
          <p class="head-sub">{t('config.description')}</p>
        </div>
        <button class="icon-btn" title={t('common.close')} onclick={closeConfig}>
          <Icon name="close" size={14} />
        </button>
      </div>

      <div class="config-path">
        <Icon name="file" size={13} />
        <button class="path" title={t('config.copyPath')} onclick={() => void copyPath()}>
          <span class="mono truncate">{view?.path ?? '…'}</span>
          <Icon name="copy" size={11} />
        </button>
        {#if view && !view.exists}
          <span class="chip">{t('config.noFile')}</span>
        {/if}
      </div>

      {#if view?.changed}
        <p class="config-notice notice notice--warn">
          <Icon name="clock" size={14} />
          <span>{t('config.changed')}</span>
        </p>
      {/if}
      {#if view?.error}
        <p class="config-notice notice notice--error">
          <Icon name="warning" size={14} />
          <span>{t('config.error', { error: view.error })}</span>
        </p>
      {/if}
      {#if error}
        <p class="config-notice notice notice--error">
          <Icon name="warning" size={14} />
          <span>{error}</span>
        </p>
      {/if}

      <div class="config-toolbar">
        <div class="config-search">
          <Icon name="search" size={13} />
          <input
            class="input"
            bind:value={query}
            placeholder={t('config.find')}
            aria-label={t('config.find')}
            autocomplete="off"
          />
        </div>
        <p class="config-note">{t('config.applyNote')}</p>
      </div>

      <div class="config-body">
        {#if loading && !view}
          <p class="muted small">{t('config.loading')}</p>
        {:else if shown.length === 0}
          <p class="muted small">{t('config.noMatch')}</p>
        {:else}
          {#each shown as section (section.name)}
            <section class="config-section">
              <h2 class="section-name mono">{section.name}</h2>
              <div class="field-list">
                {#each section.fields as field (field.key)}
                  <div
                    class="config-row"
                    class:config-row--target={field.key === target}
                    id={`config-${field.key}`}
                  >
                    <span class="row-label">
                      <span class="row-name">{name(field)}</span>
                      <span class="row-key mono">{field.key}</span>
                    </span>
                    <span class="row-value">
                      <span class="value-line">
                        <span
                          class="value mono"
                          class:value--secret={field.secret}
                          title={field.secret
                            ? t('config.secretHint')
                            : field.kind === 'path' || field.kind === 'tool'
                              ? String(field.value ?? field.lookup ?? '')
                              : ''}
                        >{valueText(field)}</span>
                        {#if field.source}
                          <span class="source source--{field.source}" title={sourceText(field)}>
                            {sourceText(field)}
                          </span>
                        {/if}
                        {#if !field.restart}
                          <span class="live" title={t('config.liveHint')}>
                            <Icon name="refresh" size={10} /> {t('config.live')}
                          </span>
                        {/if}
                      </span>
                      {#if field.kind === 'tool' || field.kind === 'path'}
                        {#if field.resolved}
                          <!-- Always the absolute path, even when the configured value already is
                               one: the second line is the effective path, the first the config. -->
                          <span class="resolved mono" title={t('config.resolvedPath')}>{field.resolved}</span>
                        {:else if field.kind === 'tool'}
                          <span class="resolved resolved--missing" title={t('config.toolMissing')}>
                            {#if field.lookup && field.lookup !== valueText(field)}{field.lookup} · {/if}{t('config.toolMissing')}
                          </span>
                        {/if}
                      {/if}
                    </span>
                  </div>
                {/each}
              </div>
            </section>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .config-overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: min(7vh, 64px) 16px 16px;
  }

  .config-bg {
    position: absolute;
    inset: 0;
    border: 0;
    background: rgb(0 0 0 / 32%);
    backdrop-filter: blur(3px);
  }

  .config {
    position: relative;
    z-index: 1;
    display: flex;
    flex-direction: column;
    width: min(760px, 100%);
    max-height: min(86vh, 900px);
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    outline: none;
    animation: config-in 0.16s var(--ease);
  }

  @keyframes config-in {
    from {
      opacity: 0;
      transform: translateY(-6px);
    }
  }

  .config-head {
    display: flex;
    flex: none;
    align-items: flex-start;
    gap: 10px;
    padding: 14px 14px 8px;
  }

  .mark {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    border-radius: var(--radius-sm);
    background: var(--bg-muted);
    color: var(--ink-2);
  }

  .head-text {
    min-width: 0;
    flex: 1;
  }

  .head-title {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.3;
  }

  .head-sub {
    margin: 2px 0 0;
    color: var(--muted);
    font-size: 12px;
    line-height: 1.45;
  }

  .config-path {
    display: flex;
    flex: none;
    align-items: center;
    gap: 7px;
    padding: 0 14px 10px;
    color: var(--muted);
    font-size: 11px;
  }

  .path {
    display: inline-flex;
    min-width: 0;
    align-items: center;
    gap: 6px;
    padding: 2px 6px;
    border: 1px solid transparent;
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--ink-2);
    font-size: 11px;
  }

  .path:hover {
    border-color: var(--line);
    background: var(--bg-muted);
  }

  .path .mono {
    overflow: hidden;
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .config-notice {
    display: flex;
    flex: none;
    align-items: flex-start;
    gap: 7px;
    margin: 0 14px 10px;
    padding: 8px 10px;
    font-size: 12px;
    line-height: 1.45;
  }

  .config-toolbar {
    display: flex;
    flex: none;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
    padding: 0 14px 10px;
    border-bottom: 1px solid var(--line);
  }

  .config-search {
    display: flex;
    flex: 1 1 220px;
    align-items: center;
    gap: 7px;
    min-width: 180px;
    padding: 0 9px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg);
    color: var(--faint);
  }

  .config-search .input {
    flex: 1;
    min-width: 0;
    padding: 6px 0;
    border: 0;
    background: transparent;
    font-size: 12px;
    outline: none;
  }

  .config-note {
    flex: 1 1 100%;
    margin: 0;
    color: var(--faint);
    font-size: 10.5px;
    line-height: 1.4;
  }

  .config-body {
    flex: 1;
    overflow-y: auto;
    padding: 4px 14px 18px;
    overscroll-behavior: contain;
  }

  .config-section {
    margin-top: 14px;
  }

  .section-name {
    position: sticky;
    top: 0;
    z-index: 1;
    margin: 0 0 4px;
    padding: 6px 0 5px;
    background: var(--bg-elevated);
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }

  .field-list {
    display: flex;
    flex-direction: column;
  }

  .config-row {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 10px 18px;
    padding: 6px 6px;
    border-radius: var(--radius-sm);
    border-bottom: 1px solid var(--line);
  }

  .config-row:last-child {
    border-bottom: 0;
  }

  .config-row:hover {
    background: var(--bg-muted);
  }

  .config-row--target {
    background: var(--accent-soft);
    box-shadow: inset 0 0 0 1px var(--accent);
  }

  .row-label {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
  }

  .row-name {
    color: var(--ink);
    font-size: 12.5px;
    line-height: 1.35;
  }

  .row-key {
    overflow: hidden;
    color: var(--faint);
    font-size: 10px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .row-value {
    display: flex;
    flex: none;
    flex-direction: column;
    align-items: flex-end;
    gap: 2px;
    max-width: 58%;
  }

  .value-line {
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 100%;
  }

  .resolved {
    max-width: 100%;
    overflow: hidden;
    color: var(--faint);
    font-size: 10px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .resolved--missing {
    color: var(--warning);
  }

  .value {
    overflow: hidden;
    color: var(--ink-2);
    font-size: 11.5px;
    text-align: right;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .value--secret {
    color: var(--muted);
    font-style: italic;
  }

  .source {
    max-width: 190px;
    overflow: hidden;
    flex: none;
    padding: 1px 6px;
    border-radius: var(--radius-pill);
    background: var(--bg-muted);
    color: var(--faint);
    font-size: 9.5px;
    letter-spacing: 0.02em;
    text-overflow: ellipsis;
    text-transform: uppercase;
    white-space: nowrap;
  }

  .source--file {
    color: var(--muted);
  }

  .source--environment {
    background: var(--warning-soft);
    color: var(--warning);
    text-transform: none;
    letter-spacing: 0;
  }

  .live {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 3px;
    color: var(--success);
    font-size: 9.5px;
    text-transform: uppercase;
  }

  @media (max-width: 640px) {
    .config-overlay {
      padding: 0;
    }

    .config {
      width: 100%;
      max-height: 100dvh;
      height: 100dvh;
      border: 0;
      border-radius: 0;
    }

    .config-row {
      flex-direction: column;
      align-items: stretch;
      gap: 3px;
    }

    .row-value {
      max-width: 100%;
      align-items: flex-start;
    }

    .value-line {
      justify-content: flex-start;
    }

    .value {
      text-align: left;
    }
  }
</style>
