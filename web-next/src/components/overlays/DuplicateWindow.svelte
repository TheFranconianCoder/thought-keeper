<script lang="ts">
  import { focusPrimaryWindow } from '../../lib/single-window';
  import { ui } from '../../lib/ui.svelte';
  import Modal from '../ui/Modal.svelte';

  function close(): void {
    ui.duplicateWindow = false;
  }
</script>

<Modal
  open={ui.duplicateWindow}
  title="Thought Keeper is already open"
  description="Only one window of the app is supported. Another window already holds the primary view."
  width="420px"
  onClose={close}
>
  <div class="dialog-actions">
    <button class="btn btn--ghost" onclick={close}>Use this window</button>
    <button
      class="btn btn--primary"
      onclick={() => {
        focusPrimaryWindow();
        window.close();
      }}>Focus the other window</button
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
