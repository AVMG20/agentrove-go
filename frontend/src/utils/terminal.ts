import type { ITerminalOptions } from '@xterm/xterm';
import type { ISearchDecorationOptions } from '@xterm/addon-search';
import type { ResolvedTheme } from '@/types/ui.types';

// xterm paints its own canvas, so it can't read surface-secondary CSS vars.
// Light/dark shades mirrored from globals.css.
const SURFACE = {
  light: { background: '#fafafa', foreground: '#27272a' },
  dark: { background: '#121214', foreground: '#e4e4e7' },
} as const;

// Built-in ANSI 16 assumes dark bg — light canvas makes white/yellow near-invisible.
// Light uses VS Code Light+; dark keeps xterm defaults.
const LIGHT_ANSI = {
  black: '#000000',
  red: '#cd3131',
  green: '#00bc00',
  yellow: '#949800',
  blue: '#0451a5',
  magenta: '#bc05bc',
  cyan: '#0598bc',
  white: '#555555',
  brightBlack: '#666666',
  brightRed: '#cd3131',
  brightGreen: '#14ce14',
  brightYellow: '#b5ba00',
  brightBlue: '#0451a5',
  brightMagenta: '#bc05bc',
  brightCyan: '#0598bc',
  brightWhite: '#a5a5a5',
} as const;

export const buildTerminalTheme = (mode: ResolvedTheme): ITerminalOptions['theme'] => {
  const isDark = mode === 'dark';
  const { background, foreground } = SURFACE[mode];
  return {
    background,
    foreground,
    cursor: foreground,
    cursorAccent: isDark ? '#0a0a0a' : '#ffffff',
    selectionBackground: isDark ? 'rgba(255, 255, 255, 0.2)' : 'rgba(0, 0, 0, 0.15)',
    selectionInactiveBackground: isDark ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.08)',
    ...(isDark ? null : LIGHT_ANSI),
  };
};

export const buildSearchDecorations = (mode: ResolvedTheme): ISearchDecorationOptions => {
  // Opaque #RRGGBB only — xterm drops the alpha byte and paints the cell
  // background solid, so the grays must keep the theme foreground readable.
  const isDark = mode === 'dark';
  return {
    matchBackground: isDark ? '#3f3f46' : '#d4d4d8',
    activeMatchBackground: isDark ? '#71717a' : '#a1a1aa',
    matchOverviewRuler: isDark ? '#71717a' : '#a1a1aa',
    activeMatchColorOverviewRuler: isDark ? '#e4e4e7' : '#27272a',
  };
};

// Key scheme owned here so Container writes and uiStore delete sweeps can't drift.
// Scope is a chat id, or `landing-<sandboxId>` for pre-chat terminals (clear-all only).
export const terminalStorageKey = (scope: string, panelKey: string): string =>
  `terminal:${scope}:${panelKey}`;

export function clearTerminalStorage(chatId?: string): void {
  const prefix = chatId ? `terminal:${chatId}:` : 'terminal:';
  for (let i = localStorage.length - 1; i >= 0; i--) {
    const key = localStorage.key(i);
    if (key?.startsWith(prefix)) localStorage.removeItem(key);
  }
}
