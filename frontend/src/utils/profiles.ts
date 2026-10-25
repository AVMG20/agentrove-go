import type { Profile, ProfileAuthMode, ProviderKind } from '@/types/profile.types';

export const PROVIDERS: ProviderKind[] = ['claude', 'codex', 'antigravity'];

export const PROVIDER_NAMES: Record<ProviderKind, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  antigravity: 'Antigravity',
};

export const PROVIDER_CLI: Record<ProviderKind, string> = {
  claude: 'claude',
  codex: 'codex',
  antigravity: 'agy',
};

// Auth modes per provider, default first (mirrors profiles.AuthModes).
export const AUTH_MODES: Record<ProviderKind, ProfileAuthMode[]> = {
  claude: ['token', 'endpoint'],
  codex: ['login', 'token'],
  antigravity: ['login', 'token'],
};

export function authModeLabel(provider: ProviderKind, mode: ProfileAuthMode): string {
  if (mode === 'endpoint') return 'Custom endpoint';
  if (mode === 'login') return provider === 'codex' ? 'ChatGPT sign-in' : 'Google sign-in';
  return provider === 'claude' ? 'Claude token' : 'API key';
}

// The small chip next to a profile's name.
export function profileChip(profile: Profile): string {
  if (profile.builtin) return 'Server';
  if (profile.auth_mode === 'endpoint') return 'Gateway';
  if (profile.auth_mode === 'login') return 'Sign-in';
  return profile.provider === 'claude' ? 'Token' : 'API key';
}

// Whether the profile has an in-box sign-in flow (mirrors profiles.loginCommand).
export function canSignIn(profile: Profile): boolean {
  if (profile.builtin) return true;
  if (profile.provider === 'claude') return profile.auth_mode === 'token';
  return profile.auth_mode === 'login';
}

// One line on a profile's state: who it's signed in as when ready, else what
// it needs.
export function profileStatusLabel(profile: Profile): string {
  switch (profile.status) {
    case 'cli_missing':
      return 'CLI not found';
    case 'needs_sign_in':
      return canSignIn(profile) ? 'Sign-in needed' : 'Token needed';
    default: {
      const parts = [profile.account?.plan, profile.account?.email].filter(Boolean);
      return parts.length > 0 ? parts.join(' · ') : 'Ready';
    }
  }
}

// Profiles by provider, in PROVIDERS order (the list keeps the server's order).
export function groupProfiles(
  profiles: Profile[],
): { provider: ProviderKind; profiles: Profile[] }[] {
  return PROVIDERS.map((provider) => ({
    provider,
    profiles: profiles.filter((p) => p.provider === provider),
  }));
}

export type ProfilePick = 'none' | 'switch' | 'new-thread';

// What picking a profile in a thread's composer does: nothing for the
// current one, switch an empty thread, and open a new thread for one with
// messages (a thread's profile is fixed once it has messages).
export function resolveProfilePick(input: {
  currentProfileId: string | null;
  pickedProfileId: string;
  hasMessages: boolean;
}): ProfilePick {
  if (input.pickedProfileId === input.currentProfileId) return 'none';
  return input.hasMessages ? 'new-thread' : 'switch';
}
