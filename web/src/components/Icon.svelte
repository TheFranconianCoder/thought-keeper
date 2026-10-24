<script lang="ts">
	// A tiny stroke icon set (16px, currentColor). Keeps the UI dependency-free.
	const { name, size = 16, class: cls = '' }: { name: string; size?: number; class?: string } = $props();

	const paths: Record<string, string> = {
		page: 'M4 2h5l3 3v9H4z M9 2v3h3',
		copy: 'M5 5h8v9H5z M7 5V3h6v2',
		search: 'M7 2a5 5 0 1 0 3.5 8.5L14 14 M7 2a5 5 0 0 1 3.5 8.5',
		spark: 'M8 1.5 9.4 6 14 7.5 9.4 9 8 13.5 6.6 9 2 7.5 6.6 6z',
		db: 'M2.5 3.5c0-1 2.5-1.8 5.5-1.8s5.5.8 5.5 1.8-2.5 1.8-5.5 1.8S2.5 4.5 2.5 3.5z M2.5 3.5v9c0 1 2.5 1.8 5.5 1.8s5.5-.8 5.5-1.8v-9 M2.5 8c0 1 2.5 1.8 5.5 1.8S13.5 9 13.5 8',
		info: 'M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13z M8 7.5v4 M8 5h.01',
		plus: 'M8 3v10 M3 8h10',
		sun: 'M8 5.2A2.8 2.8 0 1 0 8 10.8 2.8 2.8 0 0 0 8 5.2z M8 1v1.6 M8 13.4V15 M1 8h1.6 M13.4 8H15 M3 3l1.1 1.1 M11.9 11.9 13 13 M13 3l-1.1 1.1 M4.1 11.9 3 13',
		moon: 'M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z',
		file: 'M4 2h5l3 3v9H4z',
		// The source KINDS: the same page silhouette, one unmistakable mark each — three lines for
		// plain text, the down-arrow Markdown is known by, and a folded corner for the paginated
		// document. A URL keeps its favicon (or the link glyph when a site has none).
		'file-txt': 'M4 2h5l3 3v9H4z M6 8h4 M6 10h4 M6 12h2',
		'file-md': 'M4 2h5l3 3v9H4z M6.5 7.5v3.5 M5 9.5 6.5 11 8 9.5',
		'file-pdf': 'M4 2h5l3 3v9H4z M9 2v3h3',
		link: 'M6.5 9.5 9.5 6.5 M6 4.5 7.5 3a2.5 2.5 0 0 1 3.5 3.5L9.5 8 M10 11.5 8.5 13a2.5 2.5 0 0 1-3.5-3.5L6.5 8',
		trash: 'M3 4h10 M6 4V2.5h4V4 M4.5 4l.6 9.5h5.8L11.5 4',
		refresh: 'M13.5 8a5.5 5.5 0 1 1-1.6-3.9 M13.5 2v3h-3',
		check: 'M3 8.5 6.5 12 13 4',
		close: 'M4 4l8 8 M12 4l-8 8',
		// window controls
		minimise: 'M3 8h10',
		maximise: 'M4 4h8v8H4z',
		more: 'M4 8h.01 M8 8h.01 M12 8h.01',
		chevron: 'M6 4l4 4-4 4',
		back: 'M10 4l-4 4 4 4',
		forward: 'M6 4l4 4-4 4',
		// outline / tree of parts
		tree: 'M3 3h4 M3 3v10h4 M3 8h4 M9 8h4 M9 3h4 M9 13h4',
		// back to the start
		rewind: 'M7.5 4 3.5 8l4 4 M13 4l-4 4 4 4',
		// distill: one line splitting into three — one claim per statement
		distill: 'M2.5 8h3.5 M9 3.5h4.5 M9 8h4.5 M9 12.5h4.5 M6 8l3-4.5 M6 8l3 4.5',
		// capture: READ the source (an eye, not a download — the bytes are already here)
		capture: 'M1.5 8s2.5-4.5 6.5-4.5S14.5 8 14.5 8s-2.5 4.5-6.5 4.5S1.5 8 1.5 8z M8 6.2a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6',
		'chevron-down': 'M4 6l4 4 4-4',
		arrow: 'M6 4l4 4-4 4',
		// A topic is a GROUP of pages, not a page: the folder silhouette says which row opens a topic
		// page instead of one document.
		topic: 'M2 4.5h4.2l1.3 1.6H14v6.4H2z',
		// The theme in AUTO mode: the disc half in light, half in dark — the one mark that says
		// "whatever the system says" instead of naming a side.
		auto: 'M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13z',
	};

	// Icons whose mark needs a FILLED part (the auto theme's half disc). Stroke-only icons leave this
	// empty; the filled path is drawn in the same colour, without a stroke of its own.
	const filled: Record<string, string> = {
		auto: 'M8 1.5a6.5 6.5 0 0 0 0 13z',
	};
</script>

<svg
	class="ico {cls}"
	width={size}
	height={size}
	viewBox="0 0 16 16"
	fill="none"
	stroke="currentColor"
	stroke-width="1.4"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
>
	<path d={paths[name] ?? ''} />
	{#if filled[name]}<path d={filled[name]} fill="currentColor" stroke="none" />{/if}
</svg>
