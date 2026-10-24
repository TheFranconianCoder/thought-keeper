<script lang="ts">
	import Icon from './Icon.svelte';
	import { api } from '../lib/api';

	/** SourceIcon is the icon of one source, by its KIND: a Markdown file, a PDF, plain text — and a
	 *  URL the site's own FAVICON (a link glyph when a site has none).
	 *
	 *  One component, because the same source is shown in several lists (the Sources list, a page's
	 *  Sources, the bookmarks) and a source that has an icon in one place and a generic page glyph in
	 *  another reads as a different thing. `kind` is what decides; the favicon needs the hash. */
	let { kind, hash, size }: { kind: string; hash: string; size?: number } = $props();

	// Sites without a favicon: the row falls back to the link glyph, so it never loses its icon. The
	// favicon endpoint answers 404 for those, which is what the image reports back.
	let failed = $state(false);
	$effect(() => {
		void hash;
		failed = false;
	});

	const glyph = $derived(
		kind === 'url'
			? 'link'
			: kind === 'pdf'
				? 'file-pdf'
				: kind === 'txt'
					? 'file-txt'
					: kind === 'md'
						? 'file-md'
						: 'file',
	);
</script>

{#if kind === 'url' && !failed}
	<img
		class="favicon"
		src={api.faviconURL(hash)}
		alt=""
		loading="lazy"
		onerror={() => (failed = true)}
	/>
{:else}
	<Icon name={glyph} {size} />
{/if}

<style>
	.favicon {
		width: 16px;
		height: 16px;
		flex: none;
		border-radius: 3px;
		object-fit: contain;
	}
</style>
