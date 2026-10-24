/** Hashes are the router: #/claims, #/claims/<atomId>, #/wiki, #/wiki/<id>, #/ask, #/sources, #/sources/<hash>, #/status. */
export function goto(hash: string): void {
	if (location.hash !== hash) location.hash = hash;
	else window.dispatchEvent(new HashChangeEvent('hashchange'));
}

export function parseHash(hash: string): { view: string; id: string; params: URLSearchParams } {
	const raw = (hash || '#/claims').replace(/^#\/?/, '');
	const [path, query] = raw.split('?');
	const segs = path.split('/').filter(Boolean);
	const view = segs[0] ?? 'claims';
	return { view, id: segs[1] ?? '', params: new URLSearchParams(query) };
}

/** sourceHash is the address of a source PAGE: its tab and the open outline part or file are query
 *  parameters, so back/forward returns to each and the URL can be shared. */
export function sourceHash(
	hash: string,
	opts: { tab?: string; part?: string; file?: string } = {},
): string {
	const p = new URLSearchParams();
	if (opts.tab) p.set('tab', opts.tab);
	if (opts.part) p.set('part', opts.part);
	if (opts.file) p.set('file', opts.file);
	const q = p.toString();
	return `#/sources/${encodeURIComponent(hash)}${q ? '?' + q : ''}`;
}

/** withParam returns the current hash with one query parameter set (or removed when the value is
 *  empty), so an overlay or a dialog can be opened and closed without losing where the reader is. */
export function withParam(name: string, value: string): string {
	const { view, id, params } = parseHash(location.hash);
	if (value) params.set(name, value);
	else params.delete(name);
	const q = params.toString();
	return `#/${view}${id ? '/' + id : ''}${q ? '?' + q : ''}`;
}

/** withoutParams drops query parameters — closing an overlay keeps the view and the page. */
export function withoutParams(...names: string[]): string {
	const { view, id, params } = parseHash(location.hash);
	for (const n of names) params.delete(n);
	const q = params.toString();
	return `#/${view}${id ? '/' + id : ''}${q ? '?' + q : ''}`;
}
