// Single-window guard, owned by the PWA itself.
//
// A second window of the app announces itself over a BroadcastChannel; when an
// already-claimed window answers, the newcomer becomes "secondary" and is told
// about it instead of living on as a duplicate. No server involvement needed.
const CHANNEL = 'thought-keeper-ui';

let channel: BroadcastChannel | null = null;
let claimed = false;

/** claimWindow elects this window as the primary one, or reports a duplicate. */
export function claimWindow(onDuplicate: () => void): void {
	if (typeof BroadcastChannel === 'undefined') return;

	channel = new BroadcastChannel(CHANNEL);
	let primarySeen = false;

	channel.onmessage = (event) => {
		switch (event.data) {
			case 'ping':
				if (claimed) channel?.postMessage('pong');
				break;
			case 'pong':
				primarySeen = true;
				break;
			case 'focus':
				window.focus();
				break;
			case 'standdown':
				claimed = false;
				break;
		}
	};

	channel.postMessage('ping');
	window.setTimeout(() => {
		if (primarySeen) {
			channel?.postMessage('focus'); // bring the existing window forward
			onDuplicate();
			return;
		}
		claimed = true; // nobody answered: this window is the primary
	}, 250);
}

/** focusPrimary asks the primary window to come forward. */
export function focusPrimary(): void {
	channel?.postMessage('focus');
}

