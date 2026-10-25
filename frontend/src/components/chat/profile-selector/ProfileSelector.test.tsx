// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { Profile } from '@/types/profile.types';
import { ProfileSelector } from './ProfileSelector';

const h = vi.hoisted(() => ({ profiles: [] as Profile[], isLoading: false }));

vi.mock('@/hooks/useIsSplitMode', () => ({ useIsSplitMode: () => false }));
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

const profile = (over: Partial<Profile>): Profile => ({
  id: 'p',
  provider: 'claude',
  name: 'Account',
  auth_mode: 'token',
  base_url: '',
  usage_url: '',
  has_token: true,
  token_hint: '',
  is_default: false,
  builtin: false,
  login: null,
  created_at: null,
  status: 'ready',
  status_detail: '',
  account: null,
  ...over,
});

function renderSelector(props: { selected?: string; locked?: boolean; onChange?: () => void }) {
  const onProfileChange = props.onChange ?? vi.fn();
  render(
    <MemoryRouter>
      <ProfileSelector
        selectedProfileId={props.selected ?? 'builtin'}
        onProfileChange={onProfileChange}
        locked={props.locked}
      />
    </MemoryRouter>,
  );
  return onProfileChange;
}

const openPicker = (name: RegExp) => fireEvent.click(screen.getByRole('button', { name }));

beforeEach(() => {
  h.isLoading = false;
  h.profiles = [
    profile({
      id: 'builtin',
      name: 'Default',
      builtin: true,
      auth_mode: 'login',
      is_default: true,
    }),
    profile({ id: 'work', name: 'Work' }),
    profile({
      id: 'cx',
      name: 'Codex Plus',
      provider: 'codex',
      auth_mode: 'login',
      status: 'needs_sign_in',
    }),
  ];
});

afterEach(cleanup);

describe('ProfileSelector', () => {
  it('lists accounts grouped by provider with their status', () => {
    renderSelector({});
    openPicker(/Default/);
    const list = screen.getByRole('listbox');
    expect(within(list).getByText('Claude Code')).toBeTruthy();
    expect(within(list).getByText('Codex')).toBeTruthy();
    expect(within(list).getByText('Sign-in needed')).toBeTruthy();
    expect(within(list).getByRole('link', { name: /Manage accounts/ })).toBeTruthy();
  });

  it('warns that a locked thread opens a new thread, and reports the pick', () => {
    const onChange = vi.fn();
    renderSelector({ locked: true, onChange });
    openPicker(/Default/);
    expect(screen.getByText(/Picking another one starts a new thread/)).toBeTruthy();
    fireEvent.click(screen.getByRole('option', { name: /Codex Plus/ }));
    expect(onChange).toHaveBeenCalledWith('cx');
  });

  it('has no lock note on an empty thread', () => {
    renderSelector({});
    openPicker(/Default/);
    expect(screen.queryByText(/starts a new thread/)).toBeNull();
  });

  it('flags a deleted profile', () => {
    renderSelector({ selected: 'gone' });
    expect(screen.getByRole('button', { name: /Deleted account/ })).toBeTruthy();
  });

  it('shows a placeholder while loading', () => {
    h.isLoading = true;
    renderSelector({});
    expect(screen.getByLabelText('Loading accounts')).toBeTruthy();
  });
});
