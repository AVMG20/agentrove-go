// Provider profiles: the agent accounts a chat runs on (Settings → Providers).
export type ProviderKind = 'claude' | 'codex' | 'antigravity';

// claude: token | endpoint; codex, antigravity: login | token.
export type ProfileAuthMode = 'token' | 'endpoint' | 'login';

export type ProfileState = 'ready' | 'needs_sign_in' | 'cli_missing';

export type ProfileLoginStatus = 'running' | 'succeeded' | 'failed' | 'cancelled';

// A sign-in run in the box: the CLI's sign-in link, device code and output.
export interface ProfileLogin {
  profile_id: string;
  status: ProfileLoginStatus;
  url: string;
  code: string;
  output: string;
  error: string;
}

export interface ProfileAccount {
  email?: string;
  plan?: string;
}

export interface Profile {
  id: string;
  provider: ProviderKind;
  name: string;
  auth_mode: ProfileAuthMode;
  base_url: string;
  usage_url: string;
  // The token never leaves the server; only whether one is set and its end.
  has_token: boolean;
  token_hint: string;
  is_default: boolean;
  builtin: boolean;
  login: ProfileLogin | null;
  created_at: string | null;
  status: ProfileState;
  status_detail: string;
  account: ProfileAccount | null;
}

export interface ProfileInput {
  provider?: ProviderKind;
  name?: string;
  auth_mode?: ProfileAuthMode;
  base_url?: string;
  usage_url?: string;
  // Blank keeps the stored token.
  token?: string;
  clear_token?: boolean;
}

export const BUILTIN_PROFILE_ID = 'builtin';
