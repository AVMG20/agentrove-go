// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';

const h = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
  del: vi.fn(),
  getToken: vi.fn<() => string | null>(() => 'tok'),
  clearAuth: vi.fn(),
}));

vi.mock('@/lib/api', () => ({ apiClient: { post: h.post, get: h.get, delete: h.del } }));
vi.mock('@/utils/storage', () => ({
  authStorage: {
    getToken: h.getToken,
    setToken: vi.fn(),
    clearAuth: h.clearAuth,
  },
}));
vi.mock('@/utils/logger', () => ({ logger: { warn: vi.fn(), error: vi.fn() } }));

import { authService } from './authService';

beforeEach(() => {
  vi.clearAllMocks();
  h.getToken.mockReturnValue('tok');
});

describe('authService.logout', () => {
  it('ends the session and clears local auth', async () => {
    h.post.mockResolvedValueOnce(null);

    await authService.logout();

    expect(h.post).toHaveBeenCalledWith('/auth/jwt/logout');
    expect(h.clearAuth).toHaveBeenCalledTimes(1);
  });

  it('still resolves and clears local auth when the revoke request fails', async () => {
    h.post.mockRejectedValueOnce(new Error('Failed to fetch'));

    // A rejection would skip the caller's onSuccess, stranding the UI signed in with no tokens.
    await expect(authService.logout()).resolves.toBeUndefined();
    expect(h.clearAuth).toHaveBeenCalledTimes(1);
  });
});

describe('authService sessions', () => {
  it('lists sessions', async () => {
    const sessions = [{ id: 's1', current: true }];
    h.get.mockResolvedValueOnce(sessions);

    await expect(authService.listSessions()).resolves.toBe(sessions);
    expect(h.get).toHaveBeenCalledWith('/auth/sessions');
  });

  it('revokes one session by id, escaped into the path', async () => {
    h.del.mockResolvedValueOnce(null);

    await authService.revokeSession('a/b');

    expect(h.del).toHaveBeenCalledWith('/auth/sessions/a%2Fb');
  });

  it('rejects an empty session id without a request', async () => {
    await expect(authService.revokeSession('')).rejects.toThrow();
    expect(h.del).not.toHaveBeenCalled();
  });

  it('revokes all other sessions', async () => {
    h.del.mockResolvedValueOnce(null);

    await authService.revokeOtherSessions();

    expect(h.del).toHaveBeenCalledWith('/auth/sessions');
  });
});
