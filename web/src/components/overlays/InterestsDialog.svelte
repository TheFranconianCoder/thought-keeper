<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { api } from '../../lib/api';
  import { humanError } from '../../lib/api/errors';
  import type { Interest, InterestMatch, InterestPreview } from '../../lib/api/types';
  import { confirm, confirmDialog } from '../../lib/confirm.svelte';
  import { n, t, tn } from '../../lib/i18n/index.svelte';
  import { closeInterests, interestsOpen } from '../../lib/overlays';
  import { router } from '../../lib/router.svelte';
  import { interests } from '../../lib/stores/interests.svelte';
  import { toast } from '../../lib/toast.svelte';
  import Icon from '../ui/Icon.svelte';

  const open = $derived(interestsOpen());
  const KINDS = ['md', 'pdf', 'txt', 'epub', 'url'] as const;

  let mode = $state<'list' | 'edit'>('list');
  let editingSlug = $state('');
  let name = $state('');
  let terms = $state<string[]>([]);
  let termDraft = $state('');
  let exclude = $state<string[]>([]);
  let excludeDraft = $state('');
  let kinds = $state<string[]>([]);
  let enabled = $state(true);
  let queueExisting = $state(false);
  let preview = $state<InterestPreview | null>(null);
  let previewing = $state(false);
  let previewError = $state('');
  let saving = $state(false);
  let error = $state('');

  // Suggestions come from the vault's own names: the topics the agent assigns and the labels the
  // sources already carry. Nothing is invented, and no model is asked.
  let topicNames = $state<{ name: string; count: number }[]>([]);
  let tagNames = $state<{ tag: string; count: number }[]>([]);
  let suggestionsLoaded = $state(false);
  // A source the manager was opened FROM: its topics and labels become the draft's terms.
  let seededFor = $state('');

  let panel: HTMLDivElement | undefined = $state();
  let nameInput: HTMLInputElement | undefined = $state();

  const suggestions = $derived.by(() => {
    const draft = termDraft.trim().toLowerCase();
    if (draft.length < 2) return [] as { value: string; count: number; kind: 'topic' | 'tag' }[];
    const seen = new Set(terms.map((term) => term.toLowerCase()));
    const out: { value: string; count: number; kind: 'topic' | 'tag' }[] = [];
    for (const topic of topicNames) {
      if (out.length >= 6) break;
      if (seen.has(topic.name.toLowerCase()) || !topic.name.toLowerCase().includes(draft)) continue;
      out.push({ value: topic.name, count: topic.count, kind: 'topic' });
    }
    for (const tag of tagNames) {
      if (out.length >= 8) break;
      if (seen.has(tag.tag.toLowerCase()) || !tag.tag.toLowerCase().includes(draft)) continue;
      out.push({ value: tag.tag, count: tag.count, kind: 'tag' });
    }
    return out;
  });

  // A typed term that has not been committed yet is still a term: clicking Save must not ask the
  // reader to press Enter first.
  const canSave = $derived(
    name.trim().length > 0 && (terms.length > 0 || termDraft.trim().length >= 2) && !saving,
  );

  function commitDraft(): void {
    if (termDraft.trim().length >= 2) addTerm(termDraft);
  }

  $effect(() => {
    if (!open) return;
    // Opening the manager re-reads the rules: a queue entry may have changed their open counts
    // since the last look. Untracked, because the store's own update must not re-trigger this.
    untrack(() => void interests.refresh());
  });

  $effect(() => {
    if (!open) return;
    const seed = router.current.params.get('seed') ?? '';
    if (seed && seed !== seededFor) {
      seededFor = seed;
      void seedFromSource(seed);
    }
    if (!suggestionsLoaded) {
      suggestionsLoaded = true;
      void Promise.allSettled([api.claimTopics(), api.sourceFacets()]).then(([topics, facets]) => {
        if (topics.status === 'fulfilled') {
          topicNames = (topics.value.topics ?? []).map((item) => ({ name: item.topic, count: item.count }));
        }
        if (facets.status === 'fulfilled') tagNames = facets.value.tags ?? [];
      });
    }
  });

  $effect(() => {
    if (!open) {
      // Closing the overlay resets the transient editor; reopening starts at the list.
      mode = 'list';
      seededFor = '';
      resetEditor();
    }
  });

  // Same contract as the assistant's field: Escape closes, and the panel is the focus floor.
  $effect(() => {
    if (!open) return;
    void tick().then(() => panel?.focus());
    const onKey = (event: KeyboardEvent): void => {
      if (event.key !== 'Escape' || confirmDialog.request) return;
      event.preventDefault();
      closeInterests();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  });

  // Entering the editor is a new place: the name is the first thing to fill.
  $effect(() => {
    if (!open || mode !== 'edit') return;
    void tick().then(() => nameInput?.focus());
  });

  async function seedFromSource(hash: string): Promise<void> {
    try {
      const [source, atoms] = await Promise.all([api.source(hash), api.sourceAtoms(hash)]);
      const counts = new Map<string, number>();
      for (const atom of atoms.atoms ?? []) {
        if (atom.topic) counts.set(atom.topic, (counts.get(atom.topic) ?? 0) + 1);
      }
      const topics = [...counts.entries()]
        .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
        .map(([topic]) => topic.replaceAll('-', ' '))
        .slice(0, 2);
      const tags = [...(source.auto_tags ?? []), ...(source.tags ?? [])]
        .filter((tag) => !topics.includes(tag))
        .slice(0, 2);
      const chosen: string[] = [];
      for (const term of [...topics, ...tags]) {
        if (chosen.length >= 4) break;
        if (!chosen.some((item) => item.toLowerCase() === term.toLowerCase())) chosen.push(term);
      }
      name = (source.title || topics[0] || t('interests.newTitle')).slice(0, 60);
      terms = chosen;
      mode = 'edit';
    } catch {
      mode = 'edit';
    }
  }

  function resetEditor(): void {
    editingSlug = '';
    name = '';
    terms = [];
    termDraft = '';
    exclude = [];
    excludeDraft = '';
    kinds = [];
    enabled = true;
    queueExisting = false;
    preview = null;
    previewError = '';
    error = '';
  }

  function startNew(): void {
    resetEditor();
    mode = 'edit';
  }

  function startEdit(rule: Interest): void {
    resetEditor();
    editingSlug = rule.slug;
    name = rule.name;
    terms = [...rule.terms];
    exclude = [...(rule.exclude ?? [])];
    kinds = [...(rule.kinds ?? [])];
    enabled = rule.enabled;
    mode = 'edit';
  }

  function normalize(value: string): string {
    return value.trim().replace(/\s+/g, ' ').toLowerCase();
  }

  function addTerm(value: string): void {
    const term = value.trim().replace(/\s+/g, ' ');
    if (!term || term.length < 2) return;
    if (terms.some((existing) => normalize(existing) === normalize(term))) {
      termDraft = '';
      return;
    }
    if (terms.length >= 12) {
      error = t('interests.tooManyTerms');
      return;
    }
    terms = [...terms, term];
    termDraft = '';
    preview = null;
    error = '';
  }

  function addExclude(value: string): void {
    const term = value.trim().replace(/\s+/g, ' ');
    if (!term || term.length < 2) return;
    if (exclude.some((existing) => normalize(existing) === normalize(term))) {
      excludeDraft = '';
      return;
    }
    exclude = [...exclude, term];
    excludeDraft = '';
    preview = null;
  }

  function toggleKind(kind: string): void {
    kinds = kinds.includes(kind) ? kinds.filter((k) => k !== kind) : [...kinds, kind];
    preview = null;
  }

  function toggleEnabled(rule: Interest): void {
    void saveRule(rule, {
      name: rule.name,
      terms: rule.terms,
      exclude: rule.exclude,
      kinds: rule.kinds,
      enabled: !rule.enabled,
      queue_existing: false,
    });
  }

  function request(): { name: string; terms: string[]; exclude: string[]; kinds: string[]; enabled: boolean; queue_existing: boolean } {
    return {
      name: name.trim(),
      terms,
      exclude,
      kinds,
      enabled,
      queue_existing: queueExisting,
    };
  }

  async function runPreview(): Promise<void> {
    commitDraft();
    if (terms.length === 0 || previewing) return;
    previewing = true;
    previewError = '';
    try {
      preview = await api.previewInterest(request());
    } catch (e) {
      previewError = humanError(e);
      preview = null;
    } finally {
      previewing = false;
    }
  }

  async function save(): Promise<void> {
    commitDraft();
    if (!canSave) return;
    saving = true;
    error = '';
    try {
      const body = request();
      await api.saveInterest(editingSlug, body);
      await interests.refresh();
      toast(t(editingSlug ? 'interests.updated' : 'interests.created', { name: body.name }), 'success');
      mode = 'list';
      resetEditor();
    } catch (e) {
      error = humanError(e);
    } finally {
      saving = false;
    }
  }

  async function saveRule(rule: Interest, body: Parameters<typeof api.saveInterest>[1]): Promise<void> {
    try {
      await api.saveInterest(rule.slug, body);
      await interests.refresh();
    } catch (e) {
      toast(t('interests.saveFailed', { error: humanError(e) }), 'error');
    }
  }

  async function remove(rule: Interest): Promise<void> {
    const ok = await confirm({
      title: t('interests.deleteTitle', { name: rule.name }),
      message: t('interests.deleteMessage'),
      confirmLabel: t('interests.deleteConfirm'),
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteInterest(rule.slug);
      await interests.refresh();
      toast(t('interests.deleted', { name: rule.name }), 'success');
      if (editingSlug === rule.slug) {
        mode = 'list';
        resetEditor();
      }
    } catch (e) {
      toast(t('interests.deleteFailed', { error: humanError(e) }), 'error');
    }
  }

  function evidence(match: InterestMatch): string {
    const parts: string[] = [];
    if (match.topics && match.topics.length > 0) parts.push(match.topics.slice(0, 2).join(', '));
    const claims = (match.terms ?? []).reduce((sum, term) => sum + (term.claims ?? 0), 0);
    if (claims > 0) parts.push(tn('source.interestClaims', claims));
    const tags = (match.terms ?? []).flatMap((term) => term.tags ?? []);
    if (tags.length > 0) parts.push(tags.slice(0, 2).join(', '));
    return parts.join(' · ');
  }
</script>

{#if open}
  <div class="interests-overlay">
    <button class="interests-bg" aria-label={t('common.close')} onclick={closeInterests}></button>
    <div
      class="interests"
      role="dialog"
      aria-modal="true"
      aria-label={t('interests.title')}
      tabindex="-1"
      bind:this={panel}
    >
      <div class="interests-head">
        <span class="mark"><Icon name="target" size={16} /></span>
        <div class="head-text">
          <div class="head-title">
            {mode === 'list' ? t('interests.title') : editingSlug ? t('interests.editTitle') : t('interests.newTitle')}
          </div>
          <p class="head-sub">
            {mode === 'list' ? t('interests.description') : t('interests.editDescription')}
          </p>
        </div>
        <button class="icon-btn" title={t('common.close')} onclick={closeInterests}>
          <Icon name="close" size={14} />
        </button>
      </div>

      {#if mode === 'list'}
        <div class="interests-toolbar">
          <p class="interests-hint">{t('interests.queueNote')}</p>
          <button class="btn btn--primary btn--sm" onclick={startNew}>
            <Icon name="plus" size={13} /> {t('interests.new')}
          </button>
        </div>

        <div class="interests-list">
          {#if interests.error}
            <p class="notice notice--error">{interests.error}</p>
          {:else if !interests.loaded}
            <div class="stack">
              {#each Array(3) as _, index (index)}<span class="skeleton" style="height:44px"></span>{/each}
            </div>
          {:else if interests.items.length === 0}
            <div class="interest-empty">
              <p>{t('interests.emptyTitle')}</p>
              <p class="quiet">{t('interests.emptyText')}</p>
            </div>
          {:else}
            <div class="list-label">{tn('interests.rules', interests.items.length)}</div>
            {#each interests.items as rule (rule.slug)}
              <div class="interest-rule" class:interest-rule--paused={!rule.enabled}>
                <Icon name="target" size={13} />
                <span class="rule-main">
                  <span class="rule-name">
                    {rule.name}
                    {#if !rule.enabled}<span class="rule-paused">{t('interests.paused')}</span>{/if}
                  </span>
                  <span class="rule-terms">
                    {#each rule.terms as term (term)}<span class="tag">{term}</span>{/each}
                    {#if rule.exclude?.length}
                      <span class="rule-exclude">{t('interests.excludeShort', { terms: rule.exclude.join(', ') })}</span>
                    {/if}
                  </span>
                </span>
                <span class="rule-end">
                  {#if rule.open > 0}
                    <span class="rule-open" title={t('interests.openTitle')}>{tn('interests.open', rule.open)}</span>
                  {/if}
                  <span class="rule-actions">
                    <button
                      class="icon-btn"
                      title={rule.enabled ? t('interests.pause') : t('interests.resume')}
                      aria-pressed={rule.enabled}
                      onclick={() => toggleEnabled(rule)}
                    >
                      <Icon name={rule.enabled ? 'check' : 'clock'} size={13} />
                    </button>
                    <button class="icon-btn" title={t('interests.edit')} onclick={() => startEdit(rule)}>
                      <Icon name="edit" size={13} />
                    </button>
                    <button class="icon-btn icon-btn--danger" title={t('interests.delete')} onclick={() => void remove(rule)}>
                      <Icon name="trash" size={13} />
                    </button>
                  </span>
                </span>
              </div>
            {/each}
          {/if}
        </div>
      {:else}
        <form
          class="interest-editor"
          onsubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <button class="back-link" type="button" onclick={() => (mode = 'list')}>
            <Icon name="chevronLeft" size={12} /> {t('interests.title')}
          </button>

          <label class="field">
            <span>{t('interests.name')}</span>
            <input
              class="input"
              bind:this={nameInput}
              bind:value={name}
              placeholder={t('interests.namePlaceholder')}
              autocomplete="off"
            />
          </label>

          <div class="field">
            <span>{t('interests.terms')}</span>
            <div class="term-box">
              {#each terms as term (term)}
                <span class="term-chip">
                  {term}
                  <button
                    type="button"
                    class="term-remove"
                    title={t('interests.removeTerm')}
                    onclick={() => {
                      terms = terms.filter((item) => item !== term);
                      preview = null;
                    }}
                  ><Icon name="close" size={10} /></button>
                </span>
              {/each}
              <input
                class="term-input"
                bind:value={termDraft}
                placeholder={terms.length === 0 ? t('interests.termsPlaceholder') : t('interests.addTerm')}
                autocomplete="off"
                onkeydown={(event) => {
                  if (event.key === 'Enter' || event.key === ',') {
                    event.preventDefault();
                    addTerm(termDraft);
                  }
                  if (event.key === 'Backspace' && termDraft === '' && terms.length > 0) {
                    terms = terms.slice(0, -1);
                    preview = null;
                  }
                }}
              />
            </div>
            {#if suggestions.length > 0}
              <div class="suggestions">
                <span class="suggestions-label">{t('interests.suggestions')}</span>
                {#each suggestions as item (item.kind + item.value)}
                  <button
                    type="button"
                    class="chip chip--suggestion"
                    title={item.kind === 'topic' ? t('interests.suggestTopic') : t('interests.suggestTag')}
                    onclick={() => addTerm(item.value)}
                  >
                    {item.value}<span class="chip-count">{n(item.count)}</span>
                  </button>
                {/each}
              </div>
            {:else}
              <p class="hint">{t('interests.termHint')}</p>
            {/if}
          </div>

          <details class="advanced">
            <summary>{t('interests.advanced')}</summary>
            <div class="field">
              <span>{t('interests.exclude')}</span>
              <div class="term-box term-box--small">
                {#each exclude as term (term)}
                  <span class="term-chip term-chip--muted">
                    {term}
                    <button
                      type="button"
                      class="term-remove"
                      title={t('interests.removeTerm')}
                      onclick={() => {
                        exclude = exclude.filter((item) => item !== term);
                        preview = null;
                      }}
                    ><Icon name="close" size={10} /></button>
                  </span>
                {/each}
                <input
                  class="term-input"
                  bind:value={excludeDraft}
                  placeholder={t('interests.excludePlaceholder')}
                  autocomplete="off"
                  onkeydown={(event) => {
                    if (event.key === 'Enter' || event.key === ',') {
                      event.preventDefault();
                      addExclude(excludeDraft);
                    }
                  }}
                />
              </div>
              <p class="hint">{t('interests.excludeHint')}</p>
            </div>
          </details>

          <div class="field">
            <span>{t('interests.kinds')}</span>
            <div class="kind-row">
              {#each KINDS as kind (kind)}
                <button
                  type="button"
                  class="chip"
                  class:chip--accent={kinds.includes(kind)}
                  aria-pressed={kinds.includes(kind)}
                  onclick={() => toggleKind(kind)}
                >{kind}</button>
              {/each}
              {#if kinds.length === 0}<span class="hint">{t('interests.allKinds')}</span>{/if}
            </div>
          </div>

          <label class="toggle-row">
            <input type="checkbox" bind:checked={enabled} />
            <span>
              <b>{t('interests.enabled')}</b>
              <small>{t('interests.enabledHint')}</small>
            </span>
          </label>

          <div class="preview-box">
            <button class="btn btn--secondary btn--sm" type="button" disabled={previewing || terms.length === 0} onclick={() => void runPreview()}>
              {#if previewing}<span class="spinner"></span>{/if}
              <Icon name="search" size={13} /> {t('interests.preview')}
            </button>
            {#if previewError}
              <p class="notice notice--error">{previewError}</p>
            {:else if preview}
              <div class="preview-result">
                <p class="preview-count">
                  {#if preview.total === 0}
                    {t('interests.previewNone')}
                  {:else}
                    {t('interests.previewCount', { total: n(preview.total), strong: n(preview.strong), weak: n(preview.weak) })}
                  {/if}
                </p>
                {#if preview.examples.length > 0}
                  <div class="preview-rows">
                    {#each preview.examples as example (example.hash)}
                      <div class="preview-row">
                        <span class="preview-band" class:preview-band--maybe={example.band === 'maybe'}>
                          {example.band === 'fits' ? t('interests.bandFits') : t('interests.bandMaybe')}
                        </span>
                        <span class="preview-title">{example.title || example.hash?.slice(0, 12)}</span>
                        <span class="preview-evidence">{evidence(example)}</span>
                      </div>
                    {/each}
                  </div>
                {/if}
                {#if preview.strong > 0 && enabled}
                  <label class="toggle-row">
                    <input type="checkbox" bind:checked={queueExisting} />
                    <span>
                      <b>{t('interests.queueExisting')}</b>
                      <small>{t('interests.queueExistingHint', { count: n(preview.strong) })}</small>
                    </span>
                  </label>
                {/if}
              </div>
            {:else}
              <p class="hint">{t('interests.previewHint')}</p>
            {/if}
          </div>

          {#if error}<p class="notice notice--error">{error}</p>{/if}

          <div class="editor-actions">
            <span class="grow"></span>
            <button class="btn btn--ghost" type="button" onclick={() => (mode = 'list')}>{t('common.cancel')}</button>
            <button class="btn btn--primary" type="submit" disabled={!canSave}>
              {#if saving}<span class="spinner"></span>{/if}
              {editingSlug ? t('interests.save') : t('interests.create')}
            </button>
          </div>
        </form>
      {/if}
    </div>
  </div>
{/if}

<style>
  .interests-overlay {
    position: fixed;
    inset: 0;
    z-index: 60;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding: min(12vh, 110px) 16px 16px;
  }

  .interests-bg {
    position: absolute;
    inset: 0;
    border: 0;
    background: rgb(0 0 0 / 32%);
    backdrop-filter: blur(3px);
  }

  .interests {
    position: relative;
    z-index: 1;
    display: flex;
    flex-direction: column;
    width: min(640px, 100%);
    max-height: min(80vh, 780px);
    overflow: hidden;
    border: 1px solid var(--line);
    border-radius: var(--radius-lg);
    background: var(--bg-elevated);
    box-shadow: var(--shadow-pop);
    outline: none;
    animation: interests-in 0.16s var(--ease);
  }

  .interests-head {
    display: flex;
    flex: none;
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

  .interests-toolbar {
    display: flex;
    flex: none;
    align-items: center;
    gap: 10px;
    padding: 0 14px 10px;
  }

  .interests-hint {
    flex: 1;
    min-width: 0;
    margin: 0;
    color: var(--faint);
    font-size: 11.5px;
    line-height: 1.55;
  }

  .interests-list {
    min-height: 0;
    overflow-y: auto;
    border-top: 1px solid var(--line);
    padding: 8px 6px 10px;
  }

  .list-label {
    padding: 2px 8px 6px;
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  /* A rule is a ROW, not a card: no permanent border or fill, a rounded hover surface, and the
     actions only when the row is under the pointer (or the keyboard is inside it). */
  .interest-rule {
    display: flex;
    align-items: center;
    gap: 9px;
    padding: 8px;
    border-radius: var(--radius-sm);
    background: transparent;
  }

  .interest-rule:hover,
  .interest-rule:focus-within {
    background: var(--bg-hover);
  }

  .interest-rule--paused {
    opacity: 0.62;
  }

  .interest-rule > :global(.ico) {
    flex: none;
    color: var(--muted);
  }

  .rule-main {
    display: flex;
    flex: 1;
    min-width: 0;
    flex-direction: column;
    gap: 3px;
  }

  .rule-name {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--ink-2);
    font-size: 12.5px;
    font-weight: 550;
  }

  .rule-paused {
    color: var(--faint);
    font-size: 10.5px;
    font-weight: 500;
  }

  .rule-terms {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
  }

  .rule-terms :global(.tag) {
    font-size: 10.5px;
  }

  .rule-exclude {
    color: var(--faint);
    font-size: 10.5px;
  }

  /* The count and the actions share ONE right-aligned cell: the count is the resting state, the
     actions are the hover/focus state. They replace each other (a real swap, not a layer on top),
     and the fixed cell width keeps the column still. */
  .rule-end {
    display: grid;
    align-items: center;
    justify-items: end;
    flex: none;
    min-width: 80px;
  }

  .rule-open,
  .rule-actions {
    grid-area: 1 / 1;
  }

  .rule-open {
    color: var(--muted);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
    text-align: right;
    transition: opacity 0.12s var(--ease);
  }

  .rule-actions {
    display: flex;
    align-items: center;
    gap: 1px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.12s var(--ease);
  }

  .interest-rule:hover .rule-open,
  .interest-rule:focus-within .rule-open {
    opacity: 0;
  }

  .interest-rule:hover .rule-actions,
  .interest-rule:focus-within .rule-actions {
    opacity: 1;
    pointer-events: auto;
  }

  .rule-actions :global(.icon-btn) {
    width: 26px;
    height: 26px;
  }

  .interest-empty {
    padding: 14px 8px 6px;
  }

  .interest-empty p {
    margin: 0 0 6px;
    font-size: 13px;
  }

  .interest-editor {
    min-height: 0;
    overflow-y: auto;
    border-top: 1px solid var(--line);
    padding: 10px 14px 14px;
  }

  .back-link {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin: 0 0 13px;
    padding: 0;
    border: 0;
    background: transparent;
    color: var(--muted);
    font-size: 11.5px;
  }

  .back-link:hover {
    color: var(--accent);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-bottom: 15px;
  }

  .field > span {
    color: var(--muted);
    font-size: 10.5px;
    font-weight: 650;
    letter-spacing: 0.07em;
    text-transform: uppercase;
  }

  .interest-editor :global(.input) {
    height: 38px;
    padding: 0 11px;
    border-color: var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg);
    font-size: 14px;
  }

  .term-box {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
    min-height: 40px;
    padding: 6px 8px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg);
  }

  .term-box--small {
    min-height: 34px;
  }

  .term-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 4px 3px 8px;
    border-radius: var(--radius-pill);
    background: var(--accent-soft);
    color: var(--accent);
    font-size: 11.5px;
    font-weight: 550;
  }

  .term-chip--muted {
    background: var(--bg-hover);
    color: var(--muted);
  }

  .term-remove {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: transparent;
    color: inherit;
    opacity: 0.7;
  }

  .term-remove:hover {
    opacity: 1;
    background: color-mix(in srgb, currentColor 14%, transparent);
  }

  .term-input {
    flex: 1;
    min-width: 130px;
    border: 0;
    background: transparent;
    color: var(--ink);
    font: inherit;
    font-size: 12.5px;
    outline: none;
  }

  .suggestions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
  }

  .suggestions-label {
    color: var(--faint);
    font-size: 10.5px;
  }

  .chip--suggestion {
    font-size: 11px;
  }

  .hint {
    margin: 0;
    color: var(--faint);
    font-size: 11px;
    line-height: 1.5;
  }

  .advanced {
    margin: 0 0 15px;
    border-top: 1px solid var(--line);
    padding-top: 10px;
  }

  .advanced summary {
    cursor: pointer;
    color: var(--muted);
    font-size: 12px;
    margin-bottom: 10px;
  }

  .kind-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
  }

  .toggle-row {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin: 0 0 13px;
    font-size: 12.5px;
    line-height: 1.5;
  }

  .toggle-row small {
    display: block;
    color: var(--faint);
    font-size: 11px;
  }

  .preview-box {
    margin: 0 0 14px;
    padding: 11px 12px;
    border: 1px dashed var(--line-strong);
    border-radius: var(--radius);
  }

  .preview-result {
    margin-top: 10px;
  }

  .preview-count {
    margin: 0 0 8px;
    color: var(--ink-2);
    font-size: 12px;
    font-weight: 600;
  }

  .preview-rows {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .preview-row {
    display: flex;
    align-items: baseline;
    gap: 7px;
    min-width: 0;
    font-size: 11.5px;
  }

  .preview-band {
    flex: none;
    color: var(--accent);
    font-size: 10px;
    font-weight: 650;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .preview-band--maybe {
    color: var(--faint);
  }

  .preview-title {
    min-width: 0;
    overflow: hidden;
    color: var(--ink-2);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .preview-evidence {
    flex: none;
    color: var(--faint);
    font-size: 10.5px;
  }

  .editor-actions {
    display: flex;
    align-items: center;
    gap: 7px;
    padding-top: 2px;
  }

  .editor-actions .grow {
    flex: 1;
  }

  @keyframes interests-in {
    from {
      opacity: 0;
      transform: translateY(-5px) scale(0.995);
    }
  }

  @media (max-width: 640px) {
    .interests {
      max-height: calc(100dvh - 40px);
    }

    .rule-open,
    .preview-evidence {
      display: none;
    }
  }

  /* Touch has no hover: the actions are the visible state, the count makes room for them. */
  @media (hover: none) {
    .rule-open {
      opacity: 0;
    }

    .rule-actions {
      opacity: 1;
      pointer-events: auto;
    }
  }
</style>
