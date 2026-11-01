import { beforeEach, describe, expect, it, vi } from 'vitest';

const { resolve } = vi.hoisted(() => ({ resolve: vi.fn() }));
vi.mock('./api', () => ({ api: { resolveSourceLinks: resolve } }));

import { resolveSourceLinks, sourceUrlKey } from './source-links';

describe('sourceUrlKey', () => {
  it('ignores the fragment and a trailing slash', () => {
    expect(sourceUrlKey('https://example.com/a/#section')).toBe('https://example.com/a');
    expect(sourceUrlKey(' https://example.com/a ')).toBe('https://example.com/a');
    expect(sourceUrlKey('https://example.com/a/')).toBe(sourceUrlKey('https://example.com/a'));
    expect(sourceUrlKey('not a url')).toBe('not a url');
  });
});

describe('resolveSourceLinks', () => {
  beforeEach(() => {
    resolve.mockReset();
    resolve.mockImplementation(async (urls: string[]) => ({
      links: urls.filter((url) => /^https:\/\/known/.test(url)).map((url) => ({ url, hash: 'abc' })),
    }));
  });

  it('answers the URLs the vault knows and leaves the rest external', async () => {
    const links = await resolveSourceLinks(['https://known1.example/a', 'https://other1.example/b']);

    expect(links.get('https://known1.example/a')).toBe('abc');
    expect(links.has('https://other1.example/b')).toBe(false);
  });

  it('resolves a URL once per session, however it is spelled', async () => {
    await resolveSourceLinks(['https://known2.example/a']);
    const again = await resolveSourceLinks(['https://known2.example/a#part']);

    expect(again.get('https://known2.example/a#part')).toBe('abc');
    expect(resolve).toHaveBeenCalledTimes(1);
  });
});
