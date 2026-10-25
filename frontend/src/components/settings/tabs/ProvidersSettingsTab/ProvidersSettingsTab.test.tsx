// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { profileService } from '@/services/profileService';
import type { Profile } from '@/types/profile.types';
import { ProvidersSettingsTab } from './ProvidersSettingsTab';

vi.mock('@/services/profileService', () => ({
  profileService: {
    listProfiles: vi.fn(),
    createProfile: vi.fn(),
    updateProfile: vi.fn(),
    deleteProfile: vi.fn(),
    setDefaultProfile: vi.fn(),
    startLogin: vi.fn(),
    getLogin: vi.fn(),
    sendLoginInput: vi.fn(),
    cancelLogin: vi.fn(),
  },
}));
vi.mock('react-hot-toast', () => ({ default: { error: vi.fn(), success: vi.fn() } }));

// jsdom has none; the auth-mode toggle measures its segments with one.
vi.stubGlobal(
  'ResizeObserver',
  class {
    observe() {}
    unobserve() {}
    disconnect() {}
  },
);

const profile = (over: Partial<Profile>): Profile => ({
  id: 'p',
  provider: 'claude',
  name: 'Account',
  auth_mode: 'token',
  base_url: '',
  usage_url: '',
  has_token: true,
  token_hint: '…abcd',
  is_default: false,
  builtin: false,
  login: null,
  created_at: null,
  status: 'ready',
  status_detail: '',
  account: null,
  ...over,
});

const PROFILES = [
  profile({
    id: 'builtin',
    name: 'Default',
    builtin: true,
    auth_mode: 'login',
    is_default: true,
    account: { email: 'me@example.com', plan: 'max' },
  }),
  profile({ id: 'work', name: 'Work' }),
  profile({ id: 'gw', name: 'Gateway', auth_mode: 'endpoint', base_url: 'https://gw.example' }),
];

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ProvidersSettingsTab', () => {
  it('groups accounts by provider with status and the default marker', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    render(<ProvidersSettingsTab />, { wrapper });

    const claude = await screen.findByRole('region', { name: /Claude Code/ });
    expect(within(claude).getByText('· 3 accounts')).toBeTruthy();
    expect(within(claude).getByText('max · me@example.com')).toBeTruthy();
    expect(within(claude).getAllByText('Default')).toHaveLength(2); // name + marker
    // The server's account can't be edited or removed, only signed in.
    expect(within(claude).queryByRole('button', { name: 'Edit Default' })).toBeNull();
    expect(within(claude).getByRole('button', { name: 'Edit Work' })).toBeTruthy();
    expect(screen.getByRole('region', { name: /Codex/ })).toBeTruthy();
    expect(screen.getByText('No Codex accounts yet.')).toBeTruthy();
  });

  it('adds a Codex account with an API key', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    vi.mocked(profileService.createProfile).mockResolvedValue(
      profile({ id: 'cx', provider: 'codex', name: 'Team' }),
    );
    render(<ProvidersSettingsTab />, { wrapper });

    const codex = await screen.findByRole('region', { name: /Codex/ });
    fireEvent.click(within(codex).getByRole('button', { name: /Add account/ }));
    const form = within(codex).getByRole('form', { name: 'Add account' });
    fireEvent.change(within(form).getByLabelText('Name'), { target: { value: 'Team' } });
    fireEvent.click(within(form).getByRole('radio', { name: 'API key' }));

    // An API key account needs its key.
    fireEvent.click(within(form).getByRole('button', { name: 'Add account' }));
    expect(within(form).getByRole('alert').textContent).toMatch(/Enter the api key/);

    fireEvent.change(within(form).getByLabelText('API key'), { target: { value: 'sk-test' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Add account' }));
    await waitFor(() =>
      expect(profileService.createProfile).toHaveBeenCalledWith({
        provider: 'codex',
        name: 'Team',
        auth_mode: 'token',
        base_url: '',
        usage_url: '',
        token: 'sk-test',
      }),
    );
    await waitFor(() => expect(within(codex).queryByRole('form')).toBeNull());
  });

  it('keeps the token when an edit leaves it blank', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    vi.mocked(profileService.updateProfile).mockResolvedValue(PROFILES[1]);
    render(<ProvidersSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Edit Work' }));
    const form = screen.getByRole('form', { name: 'Edit Work' });
    expect(within(form).getByPlaceholderText('Blank keeps current')).toBeTruthy();
    fireEvent.change(within(form).getByLabelText('Name'), { target: { value: 'Work 2' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(profileService.updateProfile).toHaveBeenCalledWith('work', {
        name: 'Work 2',
        auth_mode: 'token',
        base_url: '',
        usage_url: '',
      }),
    );
  });

  it('shows an error with a retry when the list fails', async () => {
    vi.mocked(profileService.listProfiles).mockRejectedValue(new Error('Box unreachable'));
    render(<ProvidersSettingsTab />, { wrapper });
    expect(await screen.findByText('Box unreachable')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
  });
});
