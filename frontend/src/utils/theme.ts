import type { LucideIcon } from 'lucide-react';
import { Monitor, Moon, Sun } from 'lucide-react';
import type { Theme } from '@/types/ui.types';

export interface ThemeMeta {
  value: Theme;
  label: string;
  icon: LucideIcon;
}

// Presentation + cycle order (settings control, command menu, toggle).
export const THEMES: ThemeMeta[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
];

const THEME_BY_VALUE = Object.fromEntries(THEMES.map((t) => [t.value, t])) as Record<
  Theme,
  ThemeMeta
>;

export const getThemeMeta = (theme: Theme): ThemeMeta => THEME_BY_VALUE[theme];

export const THEME_CYCLE: Theme[] = THEMES.map((t) => t.value);

// Persisted values from before palettes were removed (dim, nord, sepia, ...) or any
// other stale string fall back to System.
export const normalizeTheme = (value: unknown): Theme =>
  THEME_CYCLE.find((theme) => theme === value) ?? 'system';
