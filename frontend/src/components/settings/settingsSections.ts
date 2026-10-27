import type { LucideIcon } from 'lucide-react';
import {
  AppWindow,
  Bot,
  Clock,
  FolderOpen,
  GitBranch,
  Key,
  MonitorSmartphone,
  ScrollText,
  Settings2,
  UserCircle,
  Zap,
} from 'lucide-react';

// Each section is a route: /settings/<id>.
export type SettingsSectionId =
  | 'general'
  | 'providers'
  | 'projects'
  | 'skills'
  | 'personas'
  | 'stream-actions'
  | 'automations'
  | 'artifacts'
  | 'env-vars'
  | 'instructions'
  | 'sessions';

export interface SettingsSection {
  id: SettingsSectionId;
  label: string;
  icon: LucideIcon;
}

export const SETTINGS_SECTIONS: SettingsSection[] = [
  { id: 'general', label: 'General', icon: Settings2 },
  { id: 'providers', label: 'Providers', icon: Bot },
  { id: 'projects', label: 'Projects', icon: FolderOpen },
  { id: 'skills', label: 'Skills', icon: Zap },
  { id: 'personas', label: 'Personas', icon: UserCircle },
  { id: 'stream-actions', label: 'Stream Actions', icon: GitBranch },
  { id: 'automations', label: 'Automations', icon: Clock },
  { id: 'artifacts', label: 'Artifacts', icon: AppWindow },
  { id: 'env-vars', label: 'Env Variables', icon: Key },
  { id: 'instructions', label: 'Instructions', icon: ScrollText },
  { id: 'sessions', label: 'Sessions', icon: MonitorSmartphone },
];

export const DEFAULT_SETTINGS_SECTION: SettingsSectionId = 'general';

// Old links from before workspaces were called projects.
const LEGACY_IDS: Record<string, SettingsSectionId> = { workspaces: 'projects' };

export function findSettingsSection(id: string | undefined): SettingsSection | undefined {
  const resolved = (id && LEGACY_IDS[id]) || id;
  return SETTINGS_SECTIONS.find((section) => section.id === resolved);
}

export const settingsPath = (id: SettingsSectionId = DEFAULT_SETTINGS_SECTION) => `/settings/${id}`;
