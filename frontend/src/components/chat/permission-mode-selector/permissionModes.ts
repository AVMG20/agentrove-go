import { ClipboardList, Lock, PencilLine, ShieldAlert, Sparkles, type LucideIcon } from 'lucide-react';
import { DEFAULT_PERMISSION_MODE, type PermissionMode } from '@/store/chatSettingsStore';

export interface PermissionModeOption {
  value: PermissionMode;
  label: string;
  description: string;
  icon: LucideIcon;
}

export const PERMISSION_MODES: PermissionModeOption[] = [
  {
    value: 'default',
    label: 'Supervised',
    description: 'Ask before commands and file changes.',
    icon: Lock,
  },
  {
    value: 'acceptEdits',
    label: 'Auto-accept edits',
    description: 'Auto-approve edits, ask before other actions.',
    icon: PencilLine,
  },
  {
    value: 'auto',
    label: 'Auto',
    description: 'An AI reviewer can approve or deny actions.',
    icon: Sparkles,
  },
  {
    value: 'bypassPermissions',
    label: 'Full access',
    description: 'Allow commands, edits and MCP tools without prompts.',
    icon: ShieldAlert,
  },
  {
    value: 'plan',
    label: 'Plan',
    description: 'Read-only: propose a plan before changing anything.',
    icon: ClipboardList,
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
