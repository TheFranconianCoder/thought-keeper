<script lang="ts">
  import { t } from '../../lib/i18n/index.svelte';
  import { focusPrimaryWindow } from '../../lib/single-window';
  import { ui } from '../../lib/ui.svelte';
  import Modal from '../ui/Modal.svelte';

  function close(): void {
    ui.duplicateWindow = false;
  }
</script>

<Modal
  open={ui.duplicateWindow}
  title={t('duplicate.title')}
  description={t('duplicate.description')}
  width="420px"
  onClose={close}
>
  <div class="dialog-actions">
    <button class="btn btn--ghost" onclick={close}>{t('duplicate.useThis')}</button>
    <button
      class="btn btn--primary"
      onclick={() => {
        focusPrimaryWindow();
        window.close();
      }}>{t('duplicate.focusOther')}</button
    >
  </div>
</Modal>

<style>
  .dialog-actions {
    display: flex;
    justify-content: flex-end;
    gap: 7px;
    padding-top: 6px;
  }
</style>
