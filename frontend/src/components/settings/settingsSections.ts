import type { LucideIcon } from 'lucide-react';
import {
  Settings2,
  Zap,
  UserCircle,
  Key,
  ScrollText,
  Clock,
  GitBranch,
  FolderOpen,
  AppWindow,
} from 'lucide-react';

export type TabKey =
  | 'general'
  | 'workspaces'
  | 'skills'
  | 'personas'
  | 'stream_actions'
  | 'automations'
  | 'artifacts'
  | 'env_vars'
  | 'instructions';

export interface SettingsNavItem {
  id: TabKey;
  label: string;
  icon: LucideIcon;
}

export const SETTINGS_NAV: SettingsNavItem[] = [
  { id: 'general', label: 'General', icon: Settings2 },
  { id: 'workspaces', label: 'Workspaces', icon: FolderOpen },
  { id: 'skills', label: 'Skills', icon: Zap },
  { id: 'personas', label: 'Personas', icon: UserCircle },
  { id: 'stream_actions', label: 'Stream Actions', icon: GitBranch },
  { id: 'automations', label: 'Automations', icon: Clock },
  { id: 'artifacts', label: 'Artifacts', icon: AppWindow },
  { id: 'env_vars', label: 'Env Variables', icon: Key },
  { id: 'instructions', label: 'Instructions', icon: ScrollText },
];

export const TAB_LABELS: Record<TabKey, string> = Object.fromEntries(
  SETTINGS_NAV.map((item) => [item.id, item.label]),
) as Record<TabKey, string>;
