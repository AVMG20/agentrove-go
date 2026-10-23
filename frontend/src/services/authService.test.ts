// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';

const h = vi.hoisted(() => ({
  post: vi.fn(),
  getRefreshToken: vi.fn<() => string | null>(() => 'refresh'),
  clearAuth: vi.fn(),
}));

vi.mock('@/lib/api', () => ({ apiClient: { post: h.post } }));
vi.mock('@/utils/storage', () => ({
  authStorage: {
    getToken: vi.fn(),
    getRefreshToken: h.getRefreshToken,
    setTokens: vi.fn(),
    clearAuth: h.clearAuth,
  },
}));
vi.mock('@/utils/logger', () => ({ logger: { warn: vi.fn(), error: vi.fn() } }));

import { authService } from './authService';

beforeEach(() => {
  vi.clearAllMocks();
  h.getRefreshToken.mockReturnValue('refresh');
});

describe('authService.logout', () => {
  it('revokes the refresh token and clears local auth', async () => {
    h.post.mockResolvedValueOnce(null);

    await authService.logout();

    expect(h.post).toHaveBeenCalledWith('/auth/jwt/logout', { refresh_token: 'refresh' });
    expect(h.clearAuth).toHaveBeenCalledTimes(1);
  });

  it('still resolves and clears local auth when the revoke request fails', async () => {
    h.post.mockRejectedValueOnce(new Error('Failed to fetch'));

    // A rejection would skip the caller's onSuccess, stranding the UI signed in with no tokens.
    await expect(authService.logout()).resolves.toBeUndefined();
    expect(h.clearAuth).toHaveBeenCalledTimes(1);
  });
});
