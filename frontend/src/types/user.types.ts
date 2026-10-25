import type { PermissionMode } from '@/store/chatSettingsStore';

export interface User {
  id: string;
  email: string;
  username: string;
  is_verified: boolean;
  is_superuser: boolean;
  email_verification_required: boolean;
}

// GET /auth/config — public, so the sign-in page can hide sign-up.
export interface AuthConfig {
  registration_enabled: boolean;
  // REGISTRATION_DISABLED pins sign-ups off; the admin switch can't override it.
  registration_locked: boolean;
}

export interface AuthResponse {
  access_token: string;
  refresh_token: string;
  token_type: string;
}

export interface CustomEnvVar {
  key: string;
  value: string;
  [key: string]: unknown;
}

// project: the project's .claude/skills; user: global, in every project;
// plugin: from an enabled Claude plugin (read-only).
export type SkillScope = 'project' | 'user' | 'plugin';

export interface CustomSkill {
  name: string;
  description: string;
  size_bytes: number;
  file_count: number;
  sources: string[];
  scope: SkillScope;
  read_only: boolean;
}

export interface Persona {
  name: string;
  content: string;
}

// Post-stream button: runs `command` on `model_id` in a new thread sharing the chat's worktree.
export interface StreamAction {
  label: string;
  enabled: boolean;
  model_id: string;
  command: string;
  persona_name: string;
  permission_mode: PermissionMode;
  thinking_mode: string;
}

export interface UserSettings {
  id: string;
  user_id: string;
  github_personal_access_token: string | null;
  custom_instructions: string | null;
  custom_env_vars: CustomEnvVar[] | null;
  personas: Persona[] | null;
  stream_actions: StreamAction[] | null;
  notifications_enabled: boolean;
  title_model_id: string;
  auto_compact_window: number;
  browser_tools_enabled: boolean;
  // Read-only: whether the server runs box browsers at all.
  browser_available: boolean;
  created_at: string;
  updated_at: string;
}

export type UserSettingsUpdate = Partial<
  Omit<UserSettings, 'id' | 'user_id' | 'created_at' | 'updated_at' | 'browser_available'>
>;
