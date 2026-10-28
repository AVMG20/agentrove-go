// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';

const h = vi.hoisted(() => ({
  getToken: vi.fn<() => string | null>(() => 'access'),
  getRefreshToken: vi.fn<() => string | null>(() => null),
  setTokens: vi.fn(async () => {}),
  syncFromStorage: vi.fn(),
  onSessionExpired: vi.fn(),
}));

vi.mock('@/utils/storage', () => ({
  authStorage: {
    getToken: h.getToken,
    getRefreshToken: h.getRefreshToken,
    setTokens: h.setTokens,
    syncFromStorage: h.syncFromStorage,
  },
}));
vi.mock('@/utils/authSession', () => ({ invalidateSessionAndRedirect: h.onSessionExpired }));

import { trimTrailingSlash, apiClient } from './api';
import { NetworkError } from '@/types/errors.types';

const json = (body: unknown, status = 200, statusText?: string) =>
  new Response(body == null ? '' : JSON.stringify(body), { status, statusText });

const fetchMock = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  h.getToken.mockReturnValue('access');
  h.getRefreshToken.mockReturnValue(null);
  vi.stubGlobal('fetch', fetchMock);
});

describe('trimTrailingSlash', () => {
  it('strips one or more trailing slashes', () => {
    expect(trimTrailingSlash('http://x/api/v1///')).toBe('http://x/api/v1');
  });

  it('leaves a slash-free url untouched', () => {
    expect(trimTrailingSlash('http://x/api/v1')).toBe('http://x/api/v1');
  });
});

describe('APIClient success + error shaping', () => {
  it('parses a JSON body on success', async () => {
    fetchMock.mockResolvedValueOnce(json({ id: 7 }));
    await expect(apiClient.get('/thing')).resolves.toEqual({ id: 7 });
  });

  it('returns null for an empty 200 body', async () => {
    fetchMock.mockResolvedValueOnce(json(null, 200));
    await expect(apiClient.get('/thing')).resolves.toBeNull();
  });

  it('throws with the JSON `message` and attaches the status', async () => {
    fetchMock.mockResolvedValueOnce(json({ message: 'boom' }, 500));
    await expect(apiClient.get('/thing')).rejects.toMatchObject({ message: 'boom', status: 500 });
  });

  it('falls back to `detail` then statusText for the error message', async () => {
    fetchMock.mockResolvedValueOnce(json({ detail: 'nope' }, 422));
    await expect(apiClient.get('/thing')).rejects.toMatchObject({ message: 'nope' });

    fetchMock.mockResolvedValueOnce(
      new Response('plain text', { status: 500, statusText: 'Boom' }),
    );
    await expect(apiClient.get('/thing')).rejects.toMatchObject({ message: 'Boom' });
  });

  it('uses a status placeholder when the error body is empty', async () => {
    fetchMock.mockResolvedValueOnce(new Response('', { status: 503 }));
    await expect(apiClient.get('/thing')).rejects.toMatchObject({
      message: 'HTTP error! status: 503',
    });
  });

  it('converts fetch rejections to NetworkError', async () => {
    fetchMock.mockRejectedValueOnce(new TypeError('Load failed'));

    await expect(apiClient.get('/thing')).rejects.toBeInstanceOf(NetworkError);
  });

  it('rethrows fetch aborts unchanged', async () => {
    const error = new DOMException('The operation was aborted', 'AbortError');
    fetchMock.mockRejectedValueOnce(error);

    await expect(apiClient.get('/thing')).rejects.toBe(error);
  });
});

describe('APIClient 401 handling', () => {
  it('refreshes the token and retries once on 401', async () => {
    h.getRefreshToken.mockReturnValue('refresh-tok');
    fetchMock
      .mockResolvedValueOnce(json({ message: 'unauth' }, 401)) // original request
      .mockResolvedValueOnce(
        json({ access_token: 'a2', refresh_token: 'r2', token_type: 'bearer' }),
      ) // refresh
      .mockResolvedValueOnce(json({ ok: true })); // retried request

    await expect(apiClient.get('/thing')).resolves.toEqual({ ok: true });
    expect(h.setTokens).toHaveBeenCalledWith('a2', 'r2');
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("refreshes with the token another tab rotated, not this tab's stale cache", async () => {
    // A bfcached/frozen tab never saw the storage event: syncing is what reveals the new token.
    h.getRefreshToken.mockReturnValue('stale-refresh');
    h.syncFromStorage.mockImplementationOnce(() => h.getRefreshToken.mockReturnValue('rotated'));
    fetchMock
      .mockResolvedValueOnce(json({ message: 'unauth' }, 401))
      .mockResolvedValueOnce(
        json({ access_token: 'a2', refresh_token: 'r2', token_type: 'bearer' }),
      )
      .mockResolvedValueOnce(json({ ok: true }));

    await expect(apiClient.get('/thing')).resolves.toEqual({ ok: true });

    const refreshCall = fetchMock.mock.calls[1];
    expect(JSON.parse(refreshCall[1].body)).toEqual({ refresh_token: 'rotated' });
    expect(h.onSessionExpired).not.toHaveBeenCalled();
  });

  it('waits for the tab that rotated the token instead of signing out', async () => {
    // Two tabs refresh at once (e.g. on wake): this tab loses, and the winning tab stores its
    // replacement only after this tab's 401 has arrived. Retry with that token, don't sign out.
    let stored = 'r1';
    h.getRefreshToken.mockImplementation(() => stored);
    fetchMock
      .mockResolvedValueOnce(json({ message: 'unauth' }, 401)) // original request
      .mockImplementationOnce(async () => {
        stored = 'r2';
        return json({ message: 'stale' }, 401);
      })
      .mockResolvedValueOnce(
        json({ access_token: 'a2', refresh_token: 'r3', token_type: 'bearer' }),
      )
      .mockResolvedValueOnce(json({ ok: true })); // retried request

    await expect(apiClient.get('/thing')).resolves.toEqual({ ok: true });
    expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ refresh_token: 'r2' });
    expect(h.onSessionExpired).not.toHaveBeenCalled();
  });

  it('retries a refresh whose response was lost instead of giving up', async () => {
    // Waking from sleep: the server may have rotated the token before the connection dropped.
    // Retrying soon (within its grace window) gets the replacement back.
    vi.useFakeTimers();
    try {
      h.getRefreshToken.mockReturnValue('r1');
      fetchMock
        .mockResolvedValueOnce(json({ message: 'unauth' }, 401)) // original request
        .mockRejectedValueOnce(new TypeError('Failed to fetch')) // refresh, response lost
        .mockResolvedValueOnce(
          json({ access_token: 'a2', refresh_token: 'r2', token_type: 'bearer' }),
        )
        .mockResolvedValueOnce(json({ ok: true })); // retried request

      const result = apiClient.get('/thing');
      await vi.runAllTimersAsync();
      await expect(result).resolves.toEqual({ ok: true });
      expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ refresh_token: 'r1' });
      expect(h.onSessionExpired).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps the session when the refresh never reaches the server', async () => {
    vi.useFakeTimers();
    try {
      h.getRefreshToken.mockReturnValue('r1');
      fetchMock
        .mockResolvedValueOnce(json({ message: 'unauth' }, 401))
        .mockRejectedValue(new TypeError('Failed to fetch'));

      const result = apiClient.get('/thing');
      const settled = expect(result).rejects.toThrow('Token refresh failed');
      await vi.runAllTimersAsync();
      await settled;
      expect(fetchMock).toHaveBeenCalledTimes(5); // request + 1 refresh + 3 retries
      expect(h.onSessionExpired).not.toHaveBeenCalled();
    } finally {
      fetchMock.mockReset();
      vi.useRealTimers();
    }
  });

  it('tears down the session on a 401 with no refresh token', async () => {
    h.getRefreshToken.mockReturnValue(null);
    fetchMock.mockResolvedValueOnce(json({ message: 'unauth' }, 401));

    await expect(apiClient.get('/thing')).rejects.toThrow('Session expired');
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
  });

  it('tears down the session when the refresh itself returns 401', async () => {
    h.getRefreshToken.mockReturnValue('refresh-tok');
    fetchMock
      .mockResolvedValueOnce(json({ message: 'unauth' }, 401)) // original request
      .mockResolvedValueOnce(json({ message: 'dead' }, 401)); // refresh -> terminal

    await expect(apiClient.get('/thing')).rejects.toThrow('Session expired');
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
  });
});

describe('getValidToken', () => {
  // Minimal JWT shape — only the base64url payload's exp claim is read.
  const jwtWithExp = (expSecs: number) => `h.${btoa(JSON.stringify({ exp: expSecs }))}.s`;

  it('reuses a cached access token that is not near expiry, without refreshing', async () => {
    const token = jwtWithExp(Math.floor(Date.now() / 1000) + 3600);
    h.getToken.mockReturnValue(token);
    h.getRefreshToken.mockReturnValue('refresh-tok');

    await expect(apiClient.getValidToken()).resolves.toBe(token);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('refreshes when the cached access token is expired', async () => {
    h.getToken.mockReturnValue(jwtWithExp(Math.floor(Date.now() / 1000) - 10));
    h.getRefreshToken.mockReturnValue('refresh-tok');
    fetchMock.mockResolvedValueOnce(
      json({ access_token: 'fresh', refresh_token: 'r2', token_type: 'bearer' }),
    );

    await expect(apiClient.getValidToken()).resolves.toBe('fresh');
    expect(h.setTokens).toHaveBeenCalledWith('fresh', 'r2');
  });

  it('refreshes when the cached token is not a decodable JWT', async () => {
    h.getToken.mockReturnValue('opaque-garbage');
    h.getRefreshToken.mockReturnValue('refresh-tok');
    fetchMock.mockResolvedValueOnce(
      json({ access_token: 'fresh', refresh_token: 'r2', token_type: 'bearer' }),
    );

    await expect(apiClient.getValidToken()).resolves.toBe('fresh');
  });

  it('returns the cached token as-is when there is no refresh token', async () => {
    h.getToken.mockReturnValue('whatever');
    h.getRefreshToken.mockReturnValue(null);

    await expect(apiClient.getValidToken()).resolves.toBe('whatever');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('tears down the session and returns null when the refresh 401s', async () => {
    h.getToken.mockReturnValue(null);
    h.getRefreshToken.mockReturnValue('refresh-tok');
    fetchMock.mockResolvedValueOnce(json({ message: 'dead' }, 401));

    await expect(apiClient.getValidToken()).resolves.toBeNull();
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
  });
});
