// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { profileService } from '@/services/profileService';
import type { Profile, ProfileLogin } from '@/types/profile.types';
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
  token_env: '',
  is_default: false,
  login: null,
  created_at: '2026-10-01T00:00:00Z',
  status: 'ready',
  status_detail: '',
  account: null,
  ...over,
});

const PROFILES = [
  profile({
    id: 'personal',
    name: 'Personal',
    auth_mode: 'login',
    is_default: true,
    account: { email: 'me@example.com', plan: 'max' },
  }),
  profile({ id: 'work', name: 'Work' }),
  profile({ id: 'gw', name: 'Gateway', auth_mode: 'endpoint', base_url: 'https://gw.example' }),
];

const routedWrapper =
  (entry = '/settings') =>
  ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[entry]}>
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        {children}
      </QueryClientProvider>
    </MemoryRouter>
  );
const wrapper = routedWrapper();

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
    expect(within(claude).getByText('Personal')).toBeTruthy();
    expect(within(claude).getByText('Default')).toBeTruthy(); // the marker
    // Every account can be edited and removed, a Claude sign-in one too.
    expect(within(claude).getByRole('button', { name: 'Edit Personal' })).toBeTruthy();
    expect(within(claude).getByRole('button', { name: 'Remove Personal' })).toBeTruthy();
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

  it('waits for the sign-in to start before polling it', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    let started: (login: ProfileLogin) => void = () => {};
    vi.mocked(profileService.startLogin).mockReturnValue(
      new Promise<ProfileLogin>((resolve) => {
        started = resolve;
      }),
    );
    vi.mocked(profileService.getLogin).mockRejectedValue(new Error('No sign-in for this profile'));
    render(<ProvidersSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Sign in Work' }));
    // Booting the box: no poll yet, so no "no longer running".
    expect(await screen.findByText(/Starting your box/)).toBeTruthy();
    expect(profileService.getLogin).not.toHaveBeenCalled();
    expect(screen.queryByText(/no longer running/)).toBeNull();

    act(() =>
      started({
        profile_id: 'work',
        status: 'running',
        url: 'https://claude.ai/oauth',
        code: '',
        output: '',
        error: '',
      }),
    );
    expect(await screen.findByRole('link', { name: /Open sign-in page/ })).toBeTruthy();
  });

  it('says what removing an account does to its threads', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    render(<ProvidersSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Work' }));
    expect(screen.getByText(/Threads on it can’t continue/)).toBeTruthy();
    expect(screen.getByText(/Its saved token is deleted/)).toBeTruthy();
    expect(screen.queryByText(/Server account/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    fireEvent.click(screen.getByRole('button', { name: 'Remove Personal' }));
    expect(screen.getByText(/sign-in files stay in your box/)).toBeTruthy();
  });

  it('sends how an endpoint takes its token', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    vi.mocked(profileService.updateProfile).mockResolvedValue(PROFILES[2]);
    render(<ProvidersSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Edit Gateway' }));
    const form = screen.getByRole('form', { name: 'Edit Gateway' });
    fireEvent.change(within(form).getByLabelText('Send the token as'), {
      target: { value: 'ANTHROPIC_API_KEY' },
    });
    fireEvent.click(within(form).getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(profileService.updateProfile).toHaveBeenCalledWith('gw', {
        name: 'Gateway',
        auth_mode: 'endpoint',
        base_url: 'https://gw.example',
        usage_url: '',
        token_env: 'ANTHROPIC_API_KEY',
      }),
    );
  });

  it('shows an error with a retry when the list fails', async () => {
    vi.mocked(profileService.listProfiles).mockRejectedValue(new Error('Box unreachable'));
    render(<ProvidersSettingsTab />, { wrapper });
    expect(await screen.findByText('Box unreachable')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
  });

  it('opens the add form of the provider in ?add=', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    render(<ProvidersSettingsTab />, { wrapper: routedWrapper('/settings?add=codex') });

    const codex = await screen.findByRole('region', { name: /Codex/ });
    expect(await within(codex).findByRole('form', { name: 'Add account' })).toBeTruthy();
    const claude = screen.getByRole('region', { name: /Claude Code/ });
    expect(within(claude).queryByRole('form')).toBeNull();
  });

  it('ignores an unknown provider in ?add=', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    render(<ProvidersSettingsTab />, { wrapper: routedWrapper('/settings?add=bogus') });

    await screen.findByRole('region', { name: /Codex/ });
    expect(screen.queryByRole('form')).toBeNull();
  });

  it('offers Claude sign-in first when adding a Claude account', async () => {
    vi.mocked(profileService.listProfiles).mockResolvedValue(PROFILES);
    render(<ProvidersSettingsTab />, { wrapper: routedWrapper('/settings?add=claude') });

    const claude = await screen.findByRole('region', { name: /Claude Code/ });
    const form = await within(claude).findByRole('form', { name: 'Add account' });
    const modes = within(form)
      .getAllByRole('radio')
      .map((radio) => radio.textContent);
    expect(modes).toEqual(['Claude sign-in', 'Claude token', 'Custom endpoint']);
    expect(within(form).getByText('After saving, sign in with your Claude account.')).toBeTruthy();
  });
});
