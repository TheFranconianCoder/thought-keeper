import { api } from './api';

/**
 * sourceUrlKey is the address a link and a source share. The fragment and a trailing slash do not
 * address a different page; the server compares by the same rule (entities.URLKey), so a link and
 * the address a page was captured from still meet.
 */
export function sourceUrlKey(raw: string): string {
  const value = raw.trim();
  try {
    const url = new URL(value);
    url.hash = '';
    url.pathname = url.pathname.replace(/\/$/, '');
    return url.toString();
  } catch {
    return value;
  }
}

/** BATCH bounds one request. A snapshot may link to hundreds of pages; the query string must stay
 *  well inside what the browser and the server accept. */
const BATCH = 75;

/** known is the session's answer: the address a source has does not change, so a URL is resolved
 *  once. A link the vault does not hold is asked again on the next render — the vault grows. */
const known = new Map<string, string>();

/**
 * resolveSourceLinks maps the URLs a rendered document links to to the vault's own sources. The
 * answer is keyed by the URL as it was passed in; a URL nothing knows is absent, so its link keeps
 * opening the web.
 */
export async function resolveSourceLinks(urls: string[]): Promise<Map<string, string>> {
  const unknown = new Map<string, string>();
  for (const raw of urls) {
    const key = sourceUrlKey(raw);
    if (!key || known.has(key) || unknown.has(key)) continue;
    unknown.set(key, raw);
  }

  const entries = [...unknown.values()];
  const batches: string[][] = [];
  for (let start = 0; start < entries.length; start += BATCH) {
    batches.push(entries.slice(start, start + BATCH));
  }
  await Promise.all(
    batches.map(async (batch) => {
      try {
        const result = await api.resolveSourceLinks(batch);
        for (const link of result.links ?? []) {
          known.set(sourceUrlKey(link.url), link.hash);
        }
      } catch {
        /* a failed read leaves the links external; the next render asks again */
      }
    }),
  );

  const out = new Map<string, string>();
  for (const raw of urls) {
    const hash = known.get(sourceUrlKey(raw));
    if (hash) out.set(raw, hash);
  }
  return out;
}
