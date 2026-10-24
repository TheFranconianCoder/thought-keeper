<script lang="ts">
	import Icon from './Icon.svelte';
	import { toast } from '../lib/actions';

	const { text, label = 'ID' }: { text: string; label?: string } = $props();

	async function copy(): Promise<void> {
		try {
			await navigator.clipboard.writeText(text);
			toast(`${label} copied`);
		} catch {
			toast(`Could not copy the ${label.toLowerCase()}`);
		}
	}
</script>

<button class="copy-id" title={`Copy ${label}`} onclick={() => void copy()}>
	<code>{text}</code>
	<Icon name="copy" size={12} />
</button>
