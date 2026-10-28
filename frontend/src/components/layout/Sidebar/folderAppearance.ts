import {
  BookOpen,
  Box,
  Briefcase,
  Code,
  Database,
  FlaskConical,
  Folder,
  Globe,
  Heart,
  Rocket,
  Star,
  Terminal,
  type LucideIcon,
} from 'lucide-react';
import styles from './folderAppearance.module.scss';

// Preset icons and colors of a project's sidebar folder. The keys are what the
// workspace stores; an unknown key (a preset since removed) falls back to the
// default, so the backend only checks their shape.
export const FOLDER_ICONS: { key: string; label: string; icon: LucideIcon }[] = [
  { key: 'folder', label: 'Folder', icon: Folder },
  { key: 'code', label: 'Code', icon: Code },
  { key: 'terminal', label: 'Terminal', icon: Terminal },
  { key: 'globe', label: 'Globe', icon: Globe },
  { key: 'rocket', label: 'Rocket', icon: Rocket },
  { key: 'book', label: 'Book', icon: BookOpen },
  { key: 'box', label: 'Box', icon: Box },
  { key: 'database', label: 'Database', icon: Database },
  { key: 'flask', label: 'Flask', icon: FlaskConical },
  { key: 'briefcase', label: 'Briefcase', icon: Briefcase },
  { key: 'star', label: 'Star', icon: Star },
  { key: 'heart', label: 'Heart', icon: Heart },
];

// Hues live in folderAppearance.module.scss, tuned per theme.
export const FOLDER_COLORS: { key: string; label: string }[] = [
  { key: 'red', label: 'Red' },
  { key: 'orange', label: 'Orange' },
  { key: 'yellow', label: 'Yellow' },
  { key: 'green', label: 'Green' },
  { key: 'teal', label: 'Teal' },
  { key: 'blue', label: 'Blue' },
  { key: 'purple', label: 'Purple' },
  { key: 'pink', label: 'Pink' },
];

export function folderIcon(key: string | null | undefined): LucideIcon {
  return FOLDER_ICONS.find((preset) => preset.key === key)?.icon ?? Folder;
}

// The preset color in effect: null for the default and unknown keys.
export function folderColorKey(key: string | null | undefined): string | null {
  return FOLDER_COLORS.find((preset) => preset.key === key)?.key ?? null;
}

// Sets --folder-color for the element and its children; undefined for the
// default (the theme's muted icon color).
export function folderColorClass(key: string | null | undefined): string | undefined {
  const preset = folderColorKey(key);
  return preset ? styles[`color-${preset}`] : undefined;
}
