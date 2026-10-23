import type { LucideIcon } from 'lucide-react';
import {
  AppWindow,
  Clock,
  FolderOpen,
  GitBranch,
  Key,
  ScrollText,
  Settings2,
  UserCircle,
  Zap,
} from 'lucide-react';

// Each section is a route: /settings/<id>.
export type SettingsSectionId =
  | 'general'
  | 'projects'
  | 'skills'
  | 'personas'
  | 'stream-actions'
  | 'automations'
  | 'artifacts'
  | 'env-vars'
  | 'instructions';

export interface SettingsSection {
  id: SettingsSectionId;
  label: string;
  icon: LucideIcon;
}

export const SETTINGS_SECTIONS: SettingsSection[] = [
  { id: 'general', label: 'General', icon: Settings2 },
  { id: 'projects', label: 'Projects', icon: FolderOpen },
  { id: 'skills', label: 'Skills', icon: Zap },
  { id: 'personas', label: 'Personas', icon: UserCircle },
  { id: 'stream-actions', label: 'Stream Actions', icon: GitBranch },
  { id: 'automations', label: 'Automations', icon: Clock },
  { id: 'artifacts', label: 'Artifacts', icon: AppWindow },
  { id: 'env-vars', label: 'Env Variables', icon: Key },
  { id: 'instructions', label: 'Instructions', icon: ScrollText },
];

export const DEFAULT_SETTINGS_SECTION: SettingsSectionId = 'general';

// Old links from before workspaces were called projects.
const LEGACY_IDS: Record<string, SettingsSectionId> = { workspaces: 'projects' };

export function findSettingsSection(id: string | undefined): SettingsSection | undefined {
  const resolved = (id && LEGACY_IDS[id]) || id;
  return SETTINGS_SECTIONS.find((section) => section.id === resolved);
}

export const settingsPath = (id: SettingsSectionId = DEFAULT_SETTINGS_SECTION) => `/settings/${id}`;
