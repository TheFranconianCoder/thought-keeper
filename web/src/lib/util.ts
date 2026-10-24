/** fmtBytes renders a byte count compactly. */
export function fmtBytes(n: number): string {
	if (n < 1024) return n + ' B';
	if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KiB';
	return (n / (1024 * 1024)).toFixed(1) + ' MiB';
}

/** relTime renders an RFC 3339 timestamp as "3h ago". */
export function relTime(iso?: string): string {
	if (!iso) return '';
	const then = Date.parse(iso);
	if (Number.isNaN(then)) return iso;
	const secs = Math.round((Date.now() - then) / 1000);
	if (secs < 60) return secs + 's ago';
	if (secs < 3600) return Math.round(secs / 60) + 'm ago';
	if (secs < 86400) return Math.round(secs / 3600) + 'h ago';
	return Math.round(secs / 86400) + 'd ago';
}


/** splitFrontmatter separates a document's YAML header from its body.
 *
 *  The header is SHOWN, not stripped: it is what a source declares about itself — a URL's address and
 *  the time it was fetched, a page's provenance — and a reader that hides it hides exactly the part
 *  that says where the text came from. */
export function splitFrontmatter(text: string): { front: string; body: string } {
	if (!text.startsWith('---')) return { front: '', body: text };
	const end = text.indexOf('\n---', 3);
	if (end < 0) return { front: '', body: text };
	// The fences are what delimits the header in the FILE; the block this returns is a rendering of the
	// metadata itself, so they are not repeated around it.
	const front = text
		.slice(3, end)
		.split('\n')
		.map((line) => line.replace(/\s+$/, ''))
		.join('\n')
		.replace(/^\n+/, '')
		.replace(/\n+$/, '');
	return { front, body: text.slice(end + 4).replace(/^\s*\n/, '') };
}
