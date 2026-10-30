import type { ConnectionState } from '../events/event-stream';
import { t } from '../i18n/index.svelte';

export const connection = $state<{
  state: ConnectionState;
  lastFrameAt: number;
  reconnects: number;
}>({
  state: 'connecting',
  lastFrameAt: 0,
  reconnects: 0,
});

export function setConnectionState(state: ConnectionState): void {
  if (state === 'live' && connection.state !== 'live') connection.reconnects += 1;
  connection.state = state;
}

export function noteFrame(): void {
  connection.lastFrameAt = Date.now();
}

export function connectionLabel(): string {
  switch (connection.state) {
    case 'live':
      return t('connection.live');
    case 'offline':
      return t('connection.offline');
    case 'reconnecting':
      return t('connection.reconnecting');
    default:
      return t('connection.connecting');
  }
}
