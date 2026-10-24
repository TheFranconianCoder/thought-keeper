<script lang="ts">
	import { mdToHtml } from '../lib/markdown';

	const { text = '', topics = [] as string[] }: { text?: string; topics?: string[] } = $props();
</script>

{@html mdToHtml(text, { topics })}
