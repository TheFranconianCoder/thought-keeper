/** ApiError is the one error shape the API produces: a stable code and a readable message. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(code: string, message: string, status = 0) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }
}

import { t } from '../i18n/index.svelte';

/** humanError turns anything thrown into one short sentence for a toast or an inline notice. */
export function humanError(error: unknown): string {
  if (error instanceof ApiError) return error.message;
  if (error instanceof DOMException && error.name === 'AbortError') return t('errors.cancelled');
  if (error instanceof Error) return error.message;
  return String(error);
}
