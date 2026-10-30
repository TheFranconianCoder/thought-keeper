import { ApiError } from './errors';

const BASE = '/api/v1';

interface ErrorBody {
  error?: { code?: string; message?: string };
}

/** request is the single JSON door to the API.
 *
 *  - It never guesses a shape: a non-2xx response becomes an ApiError with the server's code.
 *  - It does not impose a timeout. Some synchronous vault operations (reindex, rebuild-trees)
 *    are legitimately slow; a UI timeout there would turn a working repair into a false error.
 */
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set('Accept', 'application/json');
  if (init?.body !== undefined && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }

  // The API is a local live view: a cached answer to "what is in my vault?" is a lie. The service
  // worker already skips /api, and this closes the HTTP cache too.
  const response = await fetch(BASE + path, { cache: 'no-store', ...init, headers });
  const text = await response.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      /* a plain-text error or an empty body */
    }
  }

  if (!response.ok) {
    const err = (body as ErrorBody | null)?.error;
    throw new ApiError(
      err?.code ?? 'internal',
      err?.message ?? (response.statusText || `Request failed (${response.status})`),
      response.status,
    );
  }
  return body as T;
}

/** requestText is for the endpoints that answer with plain text (logs, raw files). */
async function requestText(path: string, init?: RequestInit): Promise<string> {
  const response = await fetch(BASE + path, { cache: 'no-store', ...init });
  if (!response.ok) {
    const text = await response.text();
    let message = response.statusText || `Request failed (${response.status})`;
    try {
      const body = JSON.parse(text) as ErrorBody;
      if (body.error?.message) message = body.error.message;
    } catch {
      if (text) message = text;
    }
    throw new ApiError('internal', message, response.status);
  }
  return response.text();
}

const json = (method: string, value?: unknown): RequestInit => ({
  method,
  body: value === undefined ? undefined : JSON.stringify(value),
});

export { request, requestText, json };
