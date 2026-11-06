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
  // A writable knob is edited in place: draft holds the input values, original the values the running
  // configuration has. Only the differing keys travel on Save.
  let draft = $state<Record<string, string | boolean>>({});
  let original = $state<Record<string, string | boolean>>({});
  let saving = $state(false);
  let applying = $state(false);
  let saveError = $state('');
  // A writable value is edited only on instruction: the pencil opens that row, Save commits it.
  let editing = $state<string[]>([]);

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

  // A boolean shows its DRAFT while the row is open, in the same span as the read-only value: the
  // checkbox appears beside it and never replaces the text, so the line cannot reflow.
  function boolText(field: ConfigFieldView): string {
    if (isEditing(field.key) && typeof draft[field.key] === 'boolean') {
      return draft[field.key] === true ? t('config.on') : t('config.off');
    }
    return valueText(field);
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

  // The value a text input shows: a boolean as a checkbox state, a list as a comma-separated line,
  // everything else as the string the writer expects.
  function inputValue(field: ConfigFieldView): string | boolean {
    if (field.kind === 'bool') return field.value === true;
    if (field.kind === 'list') return Array.isArray(field.value) ? field.value.join(', ') : '';
    if (field.value === null || field.value === undefined) return '';
    return String(field.value);
  }

  function initDrafts(v: ConfigView): void {
    const next: Record<string, string | boolean> = {};
    for (const section of v.sections) {
      for (const field of section.fields) {
        if (field.writable && field.source !== 'environment') next[field.key] = inputValue(field);
      }
    }
    draft = next;
    original = { ...next };
    editing = [];
  }

  function editable(field: ConfigFieldView): boolean {
    return field.writable && field.source !== 'environment';
  }

  function fieldByKey(key: string): ConfigFieldView | undefined {
    for (const section of view?.sections ?? []) {
      for (const field of section.fields) if (field.key === key) return field;
    }
    return undefined;
  }

  const dirtyKeys = $derived(Object.keys(draft).filter((key) => draft[key] !== original[key]));

  function setDraft(key: string, value: string | boolean): void {
    draft = { ...draft, [key]: value };
  }

  function isEditing(key: string): boolean {
    return editing.includes(key);
  }

  function startEdit(key: string): void {
    if (!editing.includes(key)) editing = [...editing, key];
    void tick().then(() => document.getElementById(`config-input-${key}`)?.focus());
  }

  function cancelEdit(key: string): void {
    draft = { ...draft, [key]: original[key] };
    editing = editing.filter((item) => item !== key);
  }

  function revert(): void {
    draft = { ...original };
    editing = [];
    saveError = '';
  }

  function values(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const key of dirtyKeys) {
      const field = fieldByKey(key);
      if (!field) continue;
      const raw = draft[key];
      switch (field.kind) {
        case 'bool':
          out[key] = raw === true;
          break;
        case 'int':
        case 'float': {
          const n = Number(raw);
          // An unparseable number goes to the server as the raw string, so Validate can say why.
          out[key] = Number.isFinite(n) ? n : String(raw);
          break;
        }
        case 'list':
          out[key] = String(raw)
            .split(',')
            .map((item) => item.trim())
            .filter(Boolean);
          break;
        default:
          out[key] = String(raw);
      }
    }
    return out;
  }

  async function save(): Promise<void> {
    if (!dirtyKeys.length || saving) return;
    saving = true;
    saveError = '';
    try {
      const result = await api.saveConfig(values());
      view = result.config; // the running view; changed is still true until the apply lands
      await waitApplied();
    } catch (err) {
      saveError = humanError(err);
      toast(saveError, 'error');
    } finally {
      saving = false;
    }
  }

  // The file is written before the answer, the apply runs in-process. Poll the read until the file
  // and the running configuration agree; the apply waits for the exclusive gate, so this can take
  // as long as the step that is running.
  async function waitApplied(): Promise<void> {
    applying = true;
    try {
      for (let attempt = 0; attempt < 60; attempt++) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
        const next = await api.config();
        if (!next.changed) {
          view = next;
          initDrafts(next);
          toast(t('config.applied'), 'success');
          return;
        }
      }
    } catch (err) {
      saveError = humanError(err);
    } finally {
      applying = false;
    }
  }

  // The file may have been edited over SSH; apply it as it is, without a restart.
  async function applyFile(): Promise<void> {
    if (applying || saving) return;
    saveError = '';
    try {
      const result = await api.reloadConfig();
      view = result.config;
      await waitApplied();
    } catch (err) {
      saveError = humanError(err);
      toast(saveError, 'error');
    }
  }

  async function load(): Promise<void> {
    loading = true;
    error = '';
    try {
      view = await api.config();
      initDrafts(view);
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

      {#if applying}
        <p class="config-notice notice notice--warn">
          <Icon name="refresh" size={14} />
          <span>{t('config.applying')}</span>
        </p>
      {:else if view?.changed}
        <p class="config-notice notice notice--warn">
          <Icon name="clock" size={14} />
          <span>{t('config.changed')}</span>
          <button class="btn btn--secondary btn--sm notice-action" onclick={() => void applyFile()}>
            {t('config.apply')}
          </button>
        </p>
      {/if}
      {#if saveError}
        <p class="config-notice notice notice--error">
          <Icon name="warning" size={14} />
          <span>{saveError}</span>
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
                        <!-- The value is ALWAYS in the layout: the pencil opens an input that is
                             laid over it, so the row keeps its width, height and baseline. -->
                        <span
                          class="row-control"
                          class:row-control--editing={editable(field) && isEditing(field.key)}
                        >
                          {#if editable(field) && field.kind === 'bool'}
                            <span class="row-bool">
                              <span class="value mono">{boolText(field)}</span>
                              {#if isEditing(field.key)}
                                <input
                                  id={`config-input-${field.key}`}
                                  class="row-checkbox"
                                  type="checkbox"
                                  checked={draft[field.key] === true}
                                  disabled={saving || applying}
                                  aria-label={name(field)}
                                  title={t('config.writableHint')}
                                  onchange={(event) => setDraft(field.key, event.currentTarget.checked)}
                                  onkeydown={(event) => {
                                    if (event.key === 'Enter') {
                                      event.preventDefault();
                                      void save();
                                    }
                                    if (event.key === 'Escape') {
                                      event.preventDefault();
                                      event.stopPropagation();
                                      cancelEdit(field.key);
                                    }
                                  }}
                                />
                              {/if}
                            </span>
                          {:else}
                            <span
                              class="value mono"
                              class:value--secret={field.secret}
                              class:value--hidden={editable(field) && isEditing(field.key)}
                              title={field.secret
                                ? t('config.secretHint')
                                : field.kind === 'path' || field.kind === 'tool'
                                  ? String(field.value ?? field.lookup ?? '')
                                  : ''}
                            >{valueText(field)}</span>
                            {#if editable(field) && isEditing(field.key)}
                              <input
                                id={`config-input-${field.key}`}
                                class="input row-input"
                                class:row-input--dirty={draft[field.key] !== original[field.key]}
                                type={field.kind === 'int' || field.kind === 'float' ? 'number' : 'text'}
                                value={String(draft[field.key] ?? '')}
                                disabled={saving || applying}
                                placeholder={field.kind === 'list' ? t('config.listPlaceholder') : ''}
                                oninput={(event) => setDraft(field.key, event.currentTarget.value)}
                                onkeydown={(event) => {
                                  if (event.key === 'Enter') {
                                    event.preventDefault();
                                    void save();
                                  }
                                  if (event.key === 'Escape') {
                                    event.preventDefault();
                                    event.stopPropagation();
                                    cancelEdit(field.key);
                                  }
                                }}
                              />
                            {/if}
                          {/if}
                        </span>
                        {#if field.source}
                          <span class="source source--{field.source}" title={sourceText(field)}>
                            {sourceText(field)}
                          </span>
                        {/if}
                        {#if !field.restart && field.writable && field.source !== 'environment'}
                          <span class="live" title={t('config.liveHint')}>
                            <Icon name="refresh" size={10} /> {t('config.live')}
                          </span>
                        {/if}
                        {#if editable(field)}
                          {#if isEditing(field.key)}
                            <button
                              class="icon-btn row-cancel"
                              title={t('config.cancelEdit')}
                              aria-label={t('config.cancelEdit')}
                              onclick={() => cancelEdit(field.key)}
                            >
                              <Icon name="close" size={11} />
                            </button>
                          {:else}
                            <button
                              class="icon-btn row-edit"
                              title={t('config.editValue')}
                              aria-label={t('config.editValue')}
                              onclick={() => startEdit(field.key)}
                            >
                              <Icon name="edit" size={11} />
                            </button>
                          {/if}
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

      <div class="config-foot">
        <span class="muted small">
          {#if dirtyKeys.length}{t('config.dirty', { count: dirtyKeys.length })}{:else}{t('config.unchanged')}{/if}
        </span>
        <span class="grow"></span>
        <button class="btn btn--ghost btn--sm" disabled={!dirtyKeys.length || saving || applying} onclick={revert}>
          {t('config.revert')}
        </button>
        <button
          class="btn btn--primary btn--sm"
          disabled={!dirtyKeys.length || saving || applying}
          onclick={() => void save()}
        >
          {saving ? t('config.saving') : t('config.save')}
        </button>
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

  /* The value's own text decides the box; the input is laid over it and never reflows the row. */
  .row-control {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: flex-end;
    min-width: min(150px, 38vw);
    min-height: 24px;
  }

  .value--hidden {
    visibility: hidden;
  }

  .row-input {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    padding: 2px 7px;
    font-family: var(--font-mono);
    font-size: 11.5px;
  }

  .row-input--dirty {
    border-color: var(--accent);
  }

  /* The text is the value itself; the checkbox hangs to its LEFT, outside the flow. The text keeps
     its exact position, font and width when the row is opened. */
  .row-bool {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  .row-checkbox {
    position: absolute;
    top: 50%;
    right: calc(100% + 8px);
    width: 15px;
    height: 15px;
    margin: 0;
    accent-color: var(--accent);
    cursor: pointer;
    transform: translateY(-50%);
  }

  .row-edit {
    flex: none;
    color: var(--faint);
  }

  .row-edit:hover {
    color: var(--accent);
  }

  .row-cancel {
    flex: none;
    color: var(--muted);
  }

  .notice-action {
    flex: none;
    margin-left: auto;
  }

  .config-foot {
    display: flex;
    flex: none;
    align-items: center;
    gap: 8px;
    padding: 10px 14px;
    border-top: 1px solid var(--line);
    background: var(--bg-elevated);
  }

  .config-foot .grow {
    flex: 1;
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
      flex-wrap: wrap;
      justify-content: flex-start;
    }

    .value {
      text-align: left;
    }
  }
</style>
