import { afterEach, describe, expect, it, vi } from 'vitest';
import { requestText } from './client';

describe('requestText', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the server error code instead of flattening every failure to internal', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: { code: 'not_found', message: 'no readable content' } }), {
            status: 404,
            headers: { 'Content-Type': 'application/json' },
          }),
      ),
    );

    await expect(requestText('/sources/x/raw')).rejects.toMatchObject({
      code: 'not_found',
      status: 404,
      message: 'no readable content',
    });
  });
});
