<script lang="ts">
  import { clearable } from '../../lib/clearable';
  import { addManyURLs, addPathOrURL, uploadFiles } from '../../lib/actions';
  import { isURL } from '../../lib/format';
  import { t } from '../../lib/i18n/index.svelte';
  import { addOpen, closeAdd } from '../../lib/overlays';
  import { system } from '../../lib/stores/system.svelte';
  import Modal from '../ui/Modal.svelte';
  import Icon from '../ui/Icon.svelte';

  const open = $derived(addOpen());
  let value = $state('');
  let bookmark = $state(false);
  let dragging = $state(false);
  let busy = $state(false);
  let fileInput: HTMLInputElement | undefined = $state();

  function close(): void {
    if (busy) return;
    closeAdd();
    value = '';
    bookmark = false;
    system.notice = '';
  }

  async function submit(): Promise<void> {
    const lines = value
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);
    if (lines.length === 0 || busy) return;

    busy = true;
    system.notice = '';
    try {
      if (lines.length === 1) {
        await addPathOrURL(lines[0], bookmark);
      } else if (lines.every(isURL)) {
        await addManyURLs(lines);
      } else {
        system.notice = t('add.oneAtATime');
        return;
      }
      closeAdd();
      value = '';
      bookmark = false;
    } catch {
      // The action already wrote a precise notice; keep the dialog open so the input survives.
    } finally {
      busy = false;
    }
  }

  async function onFiles(files: FileList | null): Promise<void> {
    if (!files || files.length === 0 || busy) return;
    busy = true;
    try {
      const file = files[0];
      if (files.length === 1 && /\.(txt|md|markdown)$/i.test(file.name)) {
        const text = await file.text();
        const lines = text.split('\n').map((line) => line.trim()).filter(Boolean);
        if (lines.length > 0 && lines.every(isURL)) {
          await addManyURLs(lines);
          closeAdd();
          return;
        }
      }
      await uploadFiles(files);
      closeAdd();
    } finally {
      busy = false;
    }
  }

  function onDrop(event: DragEvent): void {
    event.preventDefault();
    dragging = false;
    void onFiles(event.dataTransfer?.files ?? null);
  }

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void submit();
    }
  }
</script>

<Modal
  {open}
  title={t('add.title')}
  description={t('add.description')}
  width="540px"
  onClose={close}
>
  <form onsubmit={(event) => { event.preventDefault(); void submit(); }}>
    <label class="field">
      <span>{t('add.pathLabel')}</span>
      <textarea
        class="textarea add-input"
        use:clearable
        bind:value
        onkeydown={onKey}
        placeholder={t('add.placeholder')}
        spellcheck="false"
      ></textarea>
    </label>

    {#if isURL(value)}
      <label class="bookmark-option">
        <input type="checkbox" bind:checked={bookmark} />
        <span>
          <b>{t('add.bookmarkTitle')}</b>
          <small>{t('add.bookmarkHint')}</small>
        </span>
      </label>
    {/if}

    <div
      class="dropzone"
      class:dragging
      role="button"
      tabindex="0"
      onclick={() => fileInput?.click()}
      onkeydown={(event) => event.key === 'Enter' && fileInput?.click()}
      ondragover={(event) => {
        event.preventDefault();
        dragging = true;
      }}
      ondragleave={() => (dragging = false)}
      ondrop={onDrop}
    >
      <Icon name="upload" size={18} />
      <span>{t('add.dropPrefix')}<u>{t('add.browse')}</u>{t('add.dropSuffix')}</span>
      <small>{t('add.batchPrefix')}<code>.txt</code>{t('add.batchMiddle')}<code>.md</code>{t('add.batchSuffix')}</small>
    </div>
    <input
      type="file"
      multiple
      hidden
      accept=".md,.markdown,.txt,.pdf,.epub"
      bind:this={fileInput}
      onchange={() => void onFiles(fileInput?.files ?? null)}
    />

    {#if system.notice}
      <p class="notice notice--warn add-notice">{system.notice}</p>
    {/if}

    <div class="dialog-actions">
      <button class="btn btn--ghost" type="button" onclick={close}>{t('common.cancel')}</button>
      <button class="btn btn--primary" type="submit" disabled={busy || !value.trim()}>
        {#if busy}<span class="spinner"></span>{/if}
        {t('add.submit')}
      </button>
    </div>
  </form>
</Modal>

<style>
  .add-input {
    min-height: 132px;
    font-family: var(--font-mono);
    font-size: 12.5px;
  }

  .bookmark-option {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    margin: 0 0 13px;
    padding: 9px 10px;
    border: 1px solid var(--line);
    border-radius: var(--radius-sm);
    background: var(--bg-muted);
    cursor: pointer;
  }

  .bookmark-option input {
    margin: 2px 0 0;
    accent-color: var(--accent);
  }

  .bookmark-option span {
    display: flex;
    flex-direction: column;
    gap: 1px;
  }

  .bookmark-option b {
    color: var(--ink-2);
    font-size: 12.5px;
    font-weight: 550;
  }

  .bookmark-option small {
    color: var(--faint);
    font-size: 11.5px;
  }

  .dropzone {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 5px;
    padding: 20px 14px;
    border: 1px dashed var(--line-strong);
    border-radius: var(--radius);
    background: var(--bg-muted);
    color: var(--muted);
    font-size: 12.5px;
    text-align: center;
    cursor: pointer;
    transition: border-color 0.14s var(--ease), background 0.14s var(--ease);
  }

  .dropzone:hover,
  .dropzone.dragging {
    border-color: var(--accent);
    background: var(--accent-soft);
  }

  .dropzone small {
    color: var(--faint);
    font-size: 11px;
  }

  .add-notice {
    margin: 12px 0 0;
  }

  .dialog-actions {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    margin-top: 16px;
  }
</style>
