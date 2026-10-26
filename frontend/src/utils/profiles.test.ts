import { describe, expect, it } from 'vitest';
import type { Profile } from '@/types/profile.types';
import {
  AUTH_MODES,
  accountLine,
  authModeLabel,
  canSignIn,
  groupProfiles,
  noUsageReason,
  offersSignIn,
  profileChip,
  profileStatusLabel,
  removeProfileMessage,
  resolveProfilePick,
} from './profiles';

const profile = (over: Partial<Profile> = {}): Profile => ({
  id: 'p1',
  provider: 'claude',
  name: 'Work',
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

describe('resolveProfilePick', () => {
  it('does nothing for the current profile', () => {
    expect(
      resolveProfilePick({ currentProfileId: 'a', pickedProfileId: 'a', hasMessages: true }),
    ).toBe('none');
  });

  it('switches an empty thread in place', () => {
    expect(
      resolveProfilePick({ currentProfileId: 'a', pickedProfileId: 'b', hasMessages: false }),
    ).toBe('switch');
  });

  it('opens a new thread once the thread has messages', () => {
    expect(
      resolveProfilePick({ currentProfileId: 'a', pickedProfileId: 'b', hasMessages: true }),
    ).toBe('new-thread');
  });
});

describe('profileStatusLabel', () => {
  it('names the account when ready', () => {
    expect(profileStatusLabel(profile({ account: { email: 'a@b.c', plan: 'pro' } }))).toBe(
      'pro · a@b.c',
    );
    expect(profileStatusLabel(profile())).toBe('Ready');
  });

  it('says what is missing', () => {
    expect(profileStatusLabel(profile({ status: 'cli_missing' }))).toBe('CLI not found');
    expect(
      profileStatusLabel(
        profile({ provider: 'codex', auth_mode: 'login', status: 'needs_sign_in' }),
      ),
    ).toBe('Sign-in needed');
    expect(
      profileStatusLabel(
        profile({ provider: 'codex', auth_mode: 'token', status: 'needs_sign_in' }),
      ),
    ).toBe('Token needed');
  });
});

describe('canSignIn', () => {
  it('matches the CLI sign-in flows', () => {
    expect(canSignIn(profile({ auth_mode: 'login' }))).toBe(true);
    expect(canSignIn(profile({ auth_mode: 'token' }))).toBe(true);
    expect(canSignIn(profile({ auth_mode: 'endpoint' }))).toBe(false);
    expect(canSignIn(profile({ provider: 'codex', auth_mode: 'login' }))).toBe(true);
    expect(canSignIn(profile({ provider: 'antigravity', auth_mode: 'token' }))).toBe(false);
  });
});

describe('groupProfiles', () => {
  it('groups by provider in a fixed order', () => {
    const groups = groupProfiles([
      profile({ id: 'x', provider: 'codex' }),
      profile({ id: 'y' }),
      profile({ id: 'z', provider: 'codex' }),
    ]);
    expect(groups.map((g) => [g.provider, g.profiles.map((p) => p.id)])).toEqual([
      ['claude', ['y']],
      ['codex', ['x', 'z']],
      ['antigravity', []],
    ]);
  });
});

describe('offersSignIn', () => {
  it('skips a signed-in Antigravity account and a missing CLI', () => {
    const agy = { provider: 'antigravity' as const, auth_mode: 'login' as const };
    expect(offersSignIn(profile({ ...agy, status: 'needs_sign_in' }))).toBe(true);
    expect(offersSignIn(profile({ ...agy, status: 'ready' }))).toBe(false);
    expect(offersSignIn(profile({ ...agy, status: 'cli_missing' }))).toBe(false);
    // Signing a ready Claude or Codex account in again is fine.
    expect(offersSignIn(profile({ status: 'ready' }))).toBe(true);
    expect(offersSignIn(profile({ provider: 'codex', auth_mode: 'login' }))).toBe(true);
  });
});

describe('claude sign-in', () => {
  it('offers Claude sign-in first', () => {
    expect(AUTH_MODES.claude).toEqual(['login', 'token', 'endpoint']);
    expect(authModeLabel('claude', 'login')).toBe('Claude sign-in');
    expect(authModeLabel('codex', 'login')).toBe('ChatGPT sign-in');
    expect(authModeLabel('antigravity', 'login')).toBe('Google sign-in');
    expect(profileChip(profile({ auth_mode: 'login' }))).toBe('Sign-in');
  });
});

describe('removeProfileMessage', () => {
  it('says threads cannot continue', () => {
    expect(removeProfileMessage(profile())).toBe(
      'Remove "Work"? Threads on it can’t continue: start a new thread on another account. Its saved token is deleted.',
    );
    expect(removeProfileMessage(profile({ auth_mode: 'login' }))).toMatch(
      /sign-in files stay in your box/,
    );
  });
});

describe('accountLine', () => {
  it('joins plan and email', () => {
    expect(accountLine(profile({ account: { plan: 'Max', email: 'a@b.c' } }))).toBe('Max · a@b.c');
    expect(accountLine(profile({ account: { email: 'a@b.c' } }))).toBe('a@b.c');
    expect(accountLine(profile())).toBe('');
  });
});

describe('noUsageReason', () => {
  it('explains why an account has no usage', () => {
    expect(noUsageReason(profile({ status: 'needs_sign_in' }))).toBe(
      'Sign this account in to see its usage.',
    );
    expect(noUsageReason(profile({ provider: 'antigravity', auth_mode: 'login' }))).toBe(
      'Antigravity doesn’t report plan usage.',
    );
    expect(noUsageReason(profile({ auth_mode: 'endpoint' }))).toBe(
      'This endpoint has no usage URL.',
    );
    expect(noUsageReason(profile())).toMatch(/^Usage needs a Claude sign-in/);
    expect(noUsageReason(profile({ provider: 'codex' }))).toBe(
      'API keys have no plan limits to show.',
    );
    expect(noUsageReason(profile({ auth_mode: 'login' }))).toBe(
      'No usage reported for this account yet.',
    );
  });
});
