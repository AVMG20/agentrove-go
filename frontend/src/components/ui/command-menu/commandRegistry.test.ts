// @vitest-environment happy-dom
import { describe, it, expect, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import {
  ALL_COMMANDS,
  COMMAND_MENU_SHORTCUT,
  RESERVED_SHORTCUT_KEYS,
  SESSION_PANEL_SHORTCUT,
  SHORTCUT_MAP,
  commandShortcutLabel,
  executeCommand,
} from './commandRegistry';
import { queryKeys } from '@/hooks/queries/queryKeys';

vi.mock('react-hot-toast', () => ({ default: { success: vi.fn(), error: vi.fn() } }));
vi.mock('@/services/sandboxService', () => ({
  sandboxService: { gitPull: vi.fn(async () => ({ success: true, output: '' })) },
}));

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

describe('pull-remote', () => {
  // An open tab holding pre-pull content would otherwise save over the pulled files.
  it('invalidates cached file contents', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(queryKeys.sandbox.fileContent('sbx', 'a.txt'), { content: 'old' });
    const pull = ALL_COMMANDS.find((c) => c.id === 'pull-remote')!;

    executeCommand(pull, queryClient, vi.fn(), false, { sandboxId: 'sbx' });

    await vi.waitFor(() => {
      expect(
        queryClient.getQueryState(queryKeys.sandbox.fileContent('sbx', 'a.txt'))?.isInvalidated,
      ).toBe(true);
    });
  });
});
