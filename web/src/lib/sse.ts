import { app } from './state.svelte';

export interface StreamEvent {
	type: string;
	data?: Record<string, unknown>;
}

/** How long to wait before recreating a stream the browser has given up on. */
const RETRY_MS = 1500;

/** connectEvents opens the single SSE connection that replaces polling.
 *
 *  It is deliberately hard to shake off, because an event-driven UI has no timer that would
 *  heal a missed frame: a stream the browser stopped retrying is recreated, and every return
 *  to the window is a catch-up point. It returns a disconnect function. */
export function connectEvents(onEvent: (ev: StreamEvent) => void, onResync: () => void): () => void {
	let es: EventSource | null = null;
	let retry: ReturnType<typeof setTimeout> | undefined;
	let stopped = false;

	const start = (): void => {
		if (stopped) return;
		es = new EventSource('/api/v1/events');
		es.onopen = () => {
			const wasDown = app.connection === 'reconnecting';
			app.connection = 'live';
			if (wasDown) onResync();
		};
		es.onerror = () => {
			app.connection = 'reconnecting';
			// EventSource retries on its own, but a CLOSED stream means it has stopped: nothing
			// would ever arrive again, so recreate it.
			if (es && es.readyState === EventSource.CLOSED) {
				es.close();
				es = null;
				clearTimeout(retry);
				retry = setTimeout(start, RETRY_MS);
			}
		};
		es.onmessage = (e) => {
			try {
				onEvent(JSON.parse(e.data) as StreamEvent);
			} catch {
				/* ignore malformed frames */
			}
		};
	};

	start();

	// Coming back to the window is the catch-up: no timer, and it heals what an SSE stream
	// cannot report — a half-open connection, a frame lost while the tab was frozen.
	const onVisible = (): void => {
		if (document.visibilityState === 'visible') onResync();
	};
	document.addEventListener('visibilitychange', onVisible);
	window.addEventListener('focus', onVisible);

	return () => {
		stopped = true;
		clearTimeout(retry);
		document.removeEventListener('visibilitychange', onVisible);
		window.removeEventListener('focus', onVisible);
		es?.close();
	};
}
