// The reader's place belongs to the HISTORY ENTRY.
//
// Hash routing keeps ONE document: `#/claims/a` → `#/claims/b` is not a navigation as far as the browser is
// concerned, so it does not restore a scroll position — and the app rebuilds the view from scratch on
// every route change, which is why back/forward landed at the top every time.
//
// So the entry carries its own position: it is written as the reader scrolls (`history.state` is
// per-entry and survives back/forward), and read back when the hash changes. A NEW entry has no
// position and starts at the top — exactly the native behaviour.

let el: HTMLElement | null = null;
let queued = false;

/** remember watches one scroller and records its position on the CURRENT history entry. */
export function remember(scroller: HTMLElement): () => void {
	el = scroller;
	const onScroll = (): void => {
		if (queued) return;
		queued = true;
		requestAnimationFrame(() => {
			queued = false;
			if (!el) return;
			history.replaceState({ ...(history.state ?? {}), tk: el.scrollTop }, '');
		});
	};
	el.addEventListener('scroll', onScroll, { passive: true });
	return () => {
		el?.removeEventListener('scroll', onScroll);
		if (el === scroller) el = null;
	};
}

/** restoreNow puts the scroller back where THIS history entry was, or at the top for a new one. */
export function restoreNow(): void {
	if (!el) return;
	const state = history.state as { tk?: number } | null;
	const y = state && typeof state.tk === 'number' ? state.tk : 0;
	if (y <= 0) {
		el.scrollTop = 0;
		return;
	}
	// The view may still be filling in (a list is loaded after the route changes), so the position is
	// applied twice: once now, once after the content has had a frame to grow.
	requestAnimationFrame(() => {
		if (!el) return;
		el.scrollTop = y;
		requestAnimationFrame(() => {
			if (el) el.scrollTop = y;
		});
	});
	// A view that takes a little longer (an async list) gets one more chance — it only RAISES the
	// position, never lowers it, so it cannot pull a reader back up after they moved.
	setTimeout(() => {
		if (el && el.scrollTop < y && el.scrollHeight >= y) el.scrollTop = y;
	}, 220);
}
