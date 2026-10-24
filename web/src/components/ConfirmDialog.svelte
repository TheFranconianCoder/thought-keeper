<script lang="ts">
	import { app } from '../lib/state.svelte';
	import { onMount } from 'svelte';

	/** An in-app question, styled like the app's other dialogs. Native browser dialogs are never
	 *  used: they ignore the app's styling, block the whole page and cannot be themed. */
	let busy = $state(false);

	const req = $derived(app.ui.confirm);

	async function accept(): Promise<void> {
		const r = app.ui.confirm;
		if (!r || busy) return;
		busy = true;
		try {
			await r.onConfirm();
		} finally {
			busy = false;
			app.ui.confirm = null;
		}
	}

	function cancel(): void {
		app.ui.confirm = null;
	}

	onMount(() => {
		const onKey = (e: KeyboardEvent): void => {
			if (!app.ui.confirm) return;
			if (e.key === 'Escape') {
				e.preventDefault();
				cancel();
			}
			if (e.key === 'Enter') {
				e.preventDefault();
				void accept();
			}
		};
		window.addEventListener('keydown', onKey);
		return () => window.removeEventListener('keydown', onKey);
	});
</script>

{#if req}
	<div class="overlay">
		<button class="overlay-bg" aria-label="Cancel" onclick={cancel}></button>
		<div class="dialog" role="dialog" aria-modal="true" aria-label={req.title} tabindex="-1">
			<h2>{req.title}</h2>
			{#if req.message}<p class="sub">{req.message}</p>{/if}
			<div class="dialog-foot">
				<button class="btn" onclick={cancel}>Cancel</button>
				<button class="btn" class:btn-danger={req.danger} onclick={() => void accept()}>
					{req.confirmLabel ?? 'OK'}
				</button>
			</div>
		</div>
	</div>
{/if}
