// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Profile } from '@/types/profile.types';
import type { AccountUsage } from '@/types/usage.types';
import { AccountSwitcher } from './AccountSwitcher';

const h = vi.hoisted(() => ({
  profiles: [] as Profile[],
  isLoading: false,
  isMobile: false,
  isSplitMode: false,
  usage: {} as Record<string, AccountUsage | undefined>,
  refresh: vi.fn(),
}));

vi.mock('@/hooks/useIsSplitMode', () => ({ useIsSplitMode: () => h.isSplitMode }));
vi.mock('@/hooks/useIsMobile', () => ({ useIsMobile: () => h.isMobile }));
vi.mock('@/store/authStore', () => ({
  useAuthStore: (select: (s: { isAuthenticated: boolean }) => unknown) =>
    select({ isAuthenticated: true }),
}));
vi.mock('@/hooks/queries/useProfileQueries', () => ({
  useProfileMap: () => ({
    profiles: h.profiles,
    byId: new Map(h.profiles.map((p) => [p.id, p])),
    defaultProfile: h.profiles.find((p) => p.is_default) ?? null,
    isLoading: h.isLoading,
    error: null,
  }),
}));
vi.mock('@/hooks/queries/useUsageQueries', () => ({
  useAccountUsage: (profileId = '') => ({
    usage: h.usage[profileId],
    isRefreshing: false,
    refresh: h.refresh,
  }),
}));

const NOW = Date.parse('2026-10-23T12:00:00Z');
const HOUR = 3600;
// +30s so the floored countdown lands on the whole minute.
const inMinutes = (minutes: number) => new Date(NOW + minutes * 60_000 + 30_000).toISOString();

const profile = (over: Partial<Profile>): Profile => ({
  id: 'p',
  provider: 'claude',
  name: 'Account',
  auth_mode: 'login',
  base_url: '',
  usage_url: '',
  has_token: false,
  token_hint: '',
  token_env: '',
  is_default: false,
  login: null,
  created_at: '2026-10-01T00:00:00Z',
  status: 'ready',
  status_detail: '',
  account: null,
  ...over,
});

const okUsage = (windows: AccountUsage['windows']): AccountUsage => ({
  status: 'ok',
  provider: 'claude.ai',
  windows,
  error: null,
  fetched_at: new Date(NOW).toISOString(),
});

function renderSwitcher(
  props: { selected?: string; locked?: boolean; onChange?: (id: string) => void } = {},
) {
  const onProfileChange = props.onChange ?? vi.fn();
  render(
    <MemoryRouter>
      <AccountSwitcher
        selectedProfileId={props.selected ?? 'personal'}
        onProfileChange={onProfileChange}
        locked={props.locked}
      />
    </MemoryRouter>,
  );
  return onProfileChange;
}

const trigger = () => screen.getByRole('button', { name: /^Account:/ });
const openSwitcher = () => fireEvent.click(trigger());

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
  h.isLoading = false;
  h.isMobile = false;
  h.isSplitMode = false;
  h.refresh.mockReset();
  h.profiles = [
    profile({
      id: 'personal',
      name: 'Personal',
      is_default: true,
      account: { plan: 'Max', email: 'me@example.com' },
    }),
    profile({ id: 'work', name: 'Work', auth_mode: 'token', has_token: true }),
    profile({
      id: 'cx',
      name: 'Codex Plus',
      provider: 'codex',
      status: 'needs_sign_in',
    }),
  ];
  h.usage = {
    personal: okUsage([
      { used_percent: 39.4, resets_at: inMinutes(4 * 60 + 18), window_seconds: 5 * HOUR },
      { used_percent: 6, resets_at: inMinutes((6 * 24 + 19) * 60), window_seconds: 168 * HOUR },
    ]),
    work: {
      status: 'unavailable',
      provider: null,
      windows: [],
      error: null,
      fetched_at: new Date(NOW).toISOString(),
    },
  };
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe('AccountSwitcher', () => {
  it('links to adding an account when there are none', () => {
    h.profiles = [];
    renderSwitcher();
    const link = screen.getByRole('link', { name: 'Add an account' });
    expect(link.getAttribute('href')).toBe('/settings/providers');
    expect(screen.queryByRole('button', { name: /^Account:/ })).toBeNull();
  });

  it('shows a placeholder while loading', () => {
    h.isLoading = true;
    renderSwitcher();
    expect(screen.getByLabelText('Loading accounts')).toBeTruthy();
  });

  it('shows the account name and its usage on the trigger', () => {
    renderSwitcher();
    expect(within(trigger()).getByText('Personal')).toBeTruthy();
    expect(within(trigger()).getByText('39% 4h 18m · 6% 6d 19h')).toBeTruthy();
    expect(trigger().getAttribute('aria-label')).toBe(
      'Account: Personal, usage 39% 4h 18m · 6% 6d 19h',
    );
  });

  it('keeps the numbers off the trigger in split mode', () => {
    h.isSplitMode = true;
    renderSwitcher();
    expect(within(trigger()).queryByText(/39%/)).toBeNull();
  });

  it('opens on the usage card with every window', () => {
    renderSwitcher();
    openSwitcher();
    const card = screen.getByRole('dialog', { name: 'Claude Code usage' });
    expect(within(card).getByText('Claude Code usage')).toBeTruthy();
    expect(within(card).getByText('Updated just now')).toBeTruthy();
    expect(within(card).getByText('5-hour limit')).toBeTruthy();
    expect(within(card).getByText('39% used')).toBeTruthy();
    expect(within(card).getByText('61% remaining')).toBeTruthy();
    expect(within(card).getByText('Resets in 4h 18m')).toBeTruthy();
    expect(within(card).getByText('Weekly limit')).toBeTruthy();
    expect(within(card).getByText('6% used')).toBeTruthy();
    const meter = within(card).getByRole('meter', { name: '5-hour limit usage' });
    expect(meter.getAttribute('aria-valuenow')).toBe('39');
    expect(within(card).getByText('Max · me@example.com')).toBeTruthy();

    fireEvent.click(within(card).getByRole('button', { name: 'Refresh usage' }));
    expect(h.refresh).toHaveBeenCalled();
  });

  it('switches to the account list grouped by provider', () => {
    renderSwitcher();
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: 'Switch account (current: Personal)' }));

    const list = screen.getByRole('dialog', { name: 'Accounts' });
    const claude = within(list).getByRole('group', { name: 'Claude Code' });
    const codex = within(list).getByRole('group', { name: 'Codex' });
    expect(within(claude).getByText('Claude Code')).toBeTruthy();
    expect(
      within(claude)
        .getByRole('button', { name: /Personal/ })
        .getAttribute('aria-pressed'),
    ).toBe('true');
    expect(within(claude).getByRole('button', { name: /Work/ }).getAttribute('aria-pressed')).toBe(
      'false',
    );
    expect(within(claude).getByText('Default')).toBeTruthy();
    // Each row has its own usage windows.
    expect(within(claude).getByText('5h · 4h 18m')).toBeTruthy();
    expect(within(claude).getByText('wk · 6d 19h')).toBeTruthy();
    expect(within(codex).getByText('Sign-in needed')).toBeTruthy();
    expect(
      within(list)
        .getByRole('link', { name: /Add account/ })
        .getAttribute('href'),
    ).toBe('/settings/providers?add=claude');
    expect(within(list).getByRole('link', { name: /Manage accounts/ })).toBeTruthy();

    fireEvent.click(within(list).getByRole('button', { name: 'Back to usage' }));
    expect(screen.getByRole('dialog', { name: 'Claude Code usage' })).toBeTruthy();
  });

  it('has no group labels with accounts of one provider', () => {
    h.profiles = h.profiles.filter((p) => p.provider === 'claude');
    renderSwitcher();
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: /Switch account/ }));
    const list = screen.getByRole('dialog', { name: 'Accounts' });
    expect(within(list).queryByText('Claude Code')).toBeNull();
  });

  it('reports the picked account and closes', () => {
    const onChange = vi.fn();
    renderSwitcher({ onChange });
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: /Switch account/ }));
    fireEvent.click(screen.getByRole('button', { name: /Codex Plus/ }));
    expect(onChange).toHaveBeenCalledWith('cx');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
  });

  it('says a locked thread opens a new thread', () => {
    renderSwitcher({ locked: true });
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: /Switch account/ }));
    expect(
      screen.getByText(
        'This thread stays on its account. Picking another one starts a new thread.',
      ),
    ).toBeTruthy();
  });

  it('says threads keep their account when not locked', () => {
    renderSwitcher();
    openSwitcher();
    fireEvent.click(screen.getByRole('button', { name: /Switch account/ }));
    expect(screen.getByText('Each thread stays on the account that started it.')).toBeTruthy();
    expect(screen.queryByText(/starts a new thread/)).toBeNull();
  });

  it('explains why an account has no usage', () => {
    renderSwitcher({ selected: 'work' });
    expect(within(trigger()).queryByText(/%/)).toBeNull();
    openSwitcher();
    const card = screen.getByRole('dialog', { name: 'Claude Code usage' });
    expect(
      within(card).getByText(
        'Usage needs a Claude sign-in or an OAuth token; API keys have no plan limits.',
      ),
    ).toBeTruthy();
    expect(within(card).queryByRole('button', { name: 'Refresh usage' })).toBeNull();
  });

  it('points an account that needs signing in to Providers', () => {
    renderSwitcher({ selected: 'cx' });
    openSwitcher();
    const card = screen.getByRole('dialog', { name: 'Codex usage' });
    expect(within(card).getByText(/Sign this account in to see its usage\./)).toBeTruthy();
    expect(within(card).getByRole('link', { name: 'Open Providers' })).toBeTruthy();
  });

  it('shows the error of a failed usage fetch', () => {
    h.usage.personal = { ...okUsage([]), status: 'error', error: 'Gateway timed out' };
    renderSwitcher();
    openSwitcher();
    expect(screen.getByText('Gateway timed out')).toBeTruthy();
  });

  it('goes straight to the list when the selected account is gone', () => {
    renderSwitcher({ selected: '' });
    expect(within(trigger()).getByText('No account')).toBeTruthy();
    openSwitcher();
    const list = screen.getByRole('dialog', { name: 'Accounts' });
    expect(within(list).queryByRole('button', { name: 'Back to usage' })).toBeNull();
  });

  it('closes on Escape', () => {
    renderSwitcher();
    openSwitcher();
    expect(screen.getByRole('dialog')).toBeTruthy();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('shows only the first window, its reset to the largest unit, on mobile', () => {
    h.isMobile = true;
    renderSwitcher();
    expect(trigger().textContent).toContain('39% 4h');
    expect(trigger().textContent).not.toContain('4h 18m');
    expect(trigger().textContent).not.toContain('6%');
  });

  it('opens as a sheet on mobile', () => {
    h.isMobile = true;
    renderSwitcher();
    openSwitcher();
    const card = screen.getByRole('dialog', { name: 'Claude Code usage' });
    // Portaled out of the switcher, onto the body.
    expect(trigger().parentElement?.contains(card)).toBe(false);
    expect(document.body.contains(card)).toBe(true);
  });
});
