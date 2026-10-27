// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { authService } from '@/services/authService';
import type { AuthSession } from '@/types/user.types';
import { SessionsSettingsTab } from './SessionsSettingsTab';

vi.mock('@/services/authService', () => ({
  authService: { listSessions: vi.fn(), revokeSession: vi.fn(), revokeOtherSessions: vi.fn() },
}));

const MAC_CHROME =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36';
const LINUX_FIREFOX = 'Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0';

const session = (id: string, userAgent: string, current = false): AuthSession => ({
  id,
  user_agent: userAgent,
  ip_address: `10.0.0.${id.length}`,
  created_at: '2026-10-25T00:00:00Z',
  last_used_at: '2026-10-26T00:00:00Z',
  current,
});

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.mocked(authService.listSessions).mockReset();
  vi.mocked(authService.revokeSession).mockReset();
  vi.mocked(authService.revokeOtherSessions).mockReset();
});

describe('SessionsSettingsTab', () => {
  it('lists this device first and only offers to revoke the others', async () => {
    vi.mocked(authService.listSessions).mockResolvedValue([
      session('other', LINUX_FIREFOX),
      session('me', MAC_CHROME, true),
    ]);
    render(<SessionsSettingsTab />, { wrapper });

    const titles = await screen.findAllByRole('heading', { level: 3 });
    expect(titles.map((t) => t.textContent)).toEqual(['Chrome on macOS', 'Firefox on Linux']);
    expect(screen.getByText('This device')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Revoke Chrome on macOS' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Revoke Firefox on Linux' })).toBeTruthy();
  });

  it('revokes one session and refetches the list', async () => {
    const list = vi.mocked(authService.listSessions);
    list.mockResolvedValueOnce([session('me', MAC_CHROME, true), session('other', LINUX_FIREFOX)]);
    list.mockResolvedValueOnce([session('me', MAC_CHROME, true)]);
    vi.mocked(authService.revokeSession).mockResolvedValue();
    render(<SessionsSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Revoke Firefox on Linux' }));

    await waitFor(() => expect(screen.queryByText('Firefox on Linux')).toBeNull());
    expect(authService.revokeSession).toHaveBeenCalledWith('other');
    expect(screen.queryByRole('button', { name: 'Revoke all other sessions' })).toBeNull();
  });

  it('asks before revoking all other sessions', async () => {
    vi.mocked(authService.listSessions).mockResolvedValue([
      session('me', MAC_CHROME, true),
      session('a', LINUX_FIREFOX),
      session('bb', LINUX_FIREFOX),
    ]);
    vi.mocked(authService.revokeOtherSessions).mockResolvedValue();
    render(<SessionsSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Revoke all other sessions' }));
    expect(authService.revokeOtherSessions).not.toHaveBeenCalled();
    expect(
      screen.getByText('Sign out all 2 other devices? This device stays signed in.'),
    ).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Revoke all' }));
    await waitFor(() => expect(authService.revokeOtherSessions).toHaveBeenCalledTimes(1));
  });
});
