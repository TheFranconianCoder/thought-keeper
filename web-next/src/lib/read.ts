import { humanError } from './api/errors';
import { t } from './i18n/index.svelte';

/** readSignal is an AbortSignal.timeout where the browser has it. A local API answers in
 *  milliseconds; a read that has not answered in time is a stuck connection, not a slow vault. */
export function readSignal(ms: number): AbortSignal | undefined {
  return typeof AbortSignal !== 'undefined' && typeof AbortSignal.timeout === 'function'
    ? AbortSignal.timeout(ms)
    : undefined;
}

/** readError names a timeout for what it is instead of showing the browser's abort text. */
export function readError(error: unknown): string {
  if (error instanceof DOMException && (error.name === 'AbortError' || error.name === 'TimeoutError')) {
    return t('sources.readTimeout');
  }
  return humanError(error);
}
