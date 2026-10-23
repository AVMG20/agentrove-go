import { DEFAULT_PERMISSION_MODE, type PermissionMode } from '@/store/chatSettingsStore';

export interface PermissionModeOption {
  value: PermissionMode;
  label: string;
  description: string;
}

export const PERMISSION_MODES: PermissionModeOption[] = [
  { value: 'default', label: 'Default', description: 'Ask before edits and shell actions' },
  {
    value: 'acceptEdits',
    label: 'Accept Edits',
    description: 'Auto-accept edits during the session',
  },
  { value: 'plan', label: 'Plan', description: 'Review steps before running' },
  {
    value: 'bypassPermissions',
    label: 'Bypass Permissions',
    description: 'Skip all permission checks',
  },
];

// Persisted settings can still hold modes of agents that were removed.
export function coercePermissionMode(permissionMode: PermissionMode): PermissionMode {
  return PERMISSION_MODES.some((mode) => mode.value === permissionMode)
    ? permissionMode
    : DEFAULT_PERMISSION_MODE;
}

export function getPermissionModeOption(permissionMode: PermissionMode): PermissionModeOption {
  const effectiveMode = coercePermissionMode(permissionMode);
  const selectedMode = PERMISSION_MODES.find((mode) => mode.value === effectiveMode);

  if (!selectedMode) {
    throw new Error(`Missing permission mode option: ${effectiveMode}`);
  }

  return selectedMode;
}
