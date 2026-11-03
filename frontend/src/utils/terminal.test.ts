// @vitest-environment happy-dom
import { describe, it, expect, beforeEach } from 'vitest';
import { buildTerminalTheme, terminalStorageKey, clearTerminalStorage } from './terminal';

describe('terminalStorageKey', () => {
  it('joins chat id and panel key under the terminal namespace', () => {
    expect(terminalStorageKey('chat-1', 'main')).toBe('terminal:chat-1:main');
  });
});

describe('buildTerminalTheme', () => {
  it('uses the light base surface and light-mode accents for light', () => {
    const theme = buildTerminalTheme('light');
    expect(theme).toMatchObject({
      background: '#fafafa',
      foreground: '#27272a',
      cursor: '#27272a',
      cursorAccent: '#ffffff',
      selectionBackground: 'rgba(0, 0, 0, 0.15)',
      selectionInactiveBackground: 'rgba(0, 0, 0, 0.08)',
    });
    // Light overrides the ANSI 16 — the built-ins are dark-background colors.
    expect(theme?.white).toBe('#555555');
    expect(theme?.brightYellow).toBe('#b5ba00');
  });

  it('uses the dark base surface and dark-mode accents for dark', () => {
    const theme = buildTerminalTheme('dark');
    expect(theme).toMatchObject({
      background: '#121214',
      foreground: '#e4e4e7',
      cursorAccent: '#0a0a0a',
      selectionBackground: 'rgba(255, 255, 255, 0.2)',
    });
    // Dark keeps xterm's built-in ANSI defaults.
    expect(theme?.white).toBeUndefined();
  });
});

describe('clearTerminalStorage', () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem('terminal:c1:main', '1');
    localStorage.setItem('terminal:c1:side', '1');
    localStorage.setItem('terminal:c2:main', '1');
    localStorage.setItem('unrelated:c1', '1');
  });

  it('sweeps only the given chat and leaves other entries', () => {
    clearTerminalStorage('c1');
    expect(localStorage.getItem('terminal:c1:main')).toBeNull();
    expect(localStorage.getItem('terminal:c1:side')).toBeNull();
    expect(localStorage.getItem('terminal:c2:main')).toBe('1');
    expect(localStorage.getItem('unrelated:c1')).toBe('1');
  });

  it('sweeps every terminal entry when no chat id is given', () => {
    clearTerminalStorage();
    expect(localStorage.getItem('terminal:c1:main')).toBeNull();
    expect(localStorage.getItem('terminal:c2:main')).toBeNull();
    expect(localStorage.getItem('unrelated:c1')).toBe('1');
  });
});
