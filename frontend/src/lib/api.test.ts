// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';

const h = vi.hoisted(() => ({
  getToken: vi.fn<() => string | null>(() => 'access'),
  onSessionExpired: vi.fn(),
}));

vi.mock('@/utils/storage', () => ({
  authStorage: {
    getToken: h.getToken,
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
  it('signs out on a 401', async () => {
    fetchMock.mockResolvedValueOnce(json({ message: 'unauth' }, 401));

    await expect(apiClient.get('/thing')).rejects.toThrow('Session expired');
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('leaves a failed login to the login form', async () => {
    fetchMock.mockResolvedValueOnce(json({ detail: 'Invalid email or password' }, 401));

    await expect(apiClient.postForm('/auth/jwt/login', new FormData())).rejects.toMatchObject({
      status: 401,
    });
    expect(h.onSessionExpired).not.toHaveBeenCalled();
  });

  it('signs out on a 401 for a blob', async () => {
    fetchMock.mockResolvedValueOnce(json({ message: 'unauth' }, 401));

    await expect(apiClient.getBlob('/file')).rejects.toThrow('Session expired');
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
  });
});

describe('getValidToken', () => {
  // Minimal JWT shape — only the base64url payload's exp claim is read.
  const jwtWithExp = (exp: number) => `h.${btoa(JSON.stringify({ exp }))}.s`;
  const now = () => Math.floor(Date.now() / 1000);

  it('returns an unexpired token', async () => {
    const token = jwtWithExp(now() + 3600);
    h.getToken.mockReturnValue(token);

    await expect(apiClient.getValidToken()).resolves.toBe(token);
    expect(h.onSessionExpired).not.toHaveBeenCalled();
  });

  it('signs out and returns null for an expired token', async () => {
    h.getToken.mockReturnValue(jwtWithExp(now() - 10));

    await expect(apiClient.getValidToken()).resolves.toBeNull();
    expect(h.onSessionExpired).toHaveBeenCalledTimes(1);
  });

  it('returns null when signed out', async () => {
    h.getToken.mockReturnValue(null);

    await expect(apiClient.getValidToken()).resolves.toBeNull();
    expect(h.onSessionExpired).not.toHaveBeenCalled();
  });
});
