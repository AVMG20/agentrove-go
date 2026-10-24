// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import {
  ALL_COMMANDS,
  COMMAND_MENU_SHORTCUT,
  RESERVED_SHORTCUT_KEYS,
  SESSION_PANEL_SHORTCUT,
  SHORTCUT_MAP,
  commandShortcutLabel,
} from './commandRegistry';

const boundKeys = [
  COMMAND_MENU_SHORTCUT,
  ...ALL_COMMANDS.flatMap((cmd) => (cmd.shortcut ? [cmd.shortcut] : [])),
];

describe('global shortcuts', () => {
  it('never binds a browser-reserved Mod+Shift chord', () => {
    const reserved = boundKeys.filter((key) => RESERVED_SHORTCUT_KEYS.has(key.toLowerCase()));
    expect(reserved).toEqual([]);
  });

  // The reviewed collisions must stay covered by the reserved list itself.
  it('reserves the chords browsers keep for themselves', () => {
    for (const key of ['w', 'n', 't', 'p', 'c', 'j', 'i', 'g']) {
      expect(RESERVED_SHORTCUT_KEYS.has(key)).toBe(true);
    }
  });

  it('binds each key once, the command menu included', () => {
    expect(new Set(boundKeys).size).toBe(boundKeys.length);
    // Every command chord resolves back to its own command.
    for (const cmd of ALL_COMMANDS) {
      if (!cmd.shortcut) continue;
      const code = /^[a-z]$/.test(cmd.shortcut)
        ? `Key${cmd.shortcut.toUpperCase()}`
        : { '.': 'Period', ',': 'Comma' }[cmd.shortcut];
      expect(code && SHORTCUT_MAP.get(code)?.id).toBe(cmd.id);
    }
  });

  it('keeps the session panel hint on its own command', () => {
    const toggle = ALL_COMMANDS.find((cmd) => cmd.id === 'toggle-session-panel');
    expect(toggle?.shortcut).toBe(SESSION_PANEL_SHORTCUT);
  });

  it('labels only commands that have a chord', () => {
    expect(commandShortcutLabel('new-thread')).toMatch(/⇧O$/);
    expect(commandShortcutLabel('create-commit')).toBeUndefined();
  });
});
