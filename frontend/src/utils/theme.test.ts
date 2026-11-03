import { describe, it, expect } from 'vitest';
import { THEMES, THEME_CYCLE, getThemeMeta, normalizeTheme } from './theme';

describe('getThemeMeta', () => {
  it('returns the matching meta for a known theme', () => {
    const meta = getThemeMeta('light');
    expect(meta.value).toBe('light');
    expect(meta.label).toBe('Light');
    expect(meta.icon).toBeDefined();
  });
});

describe('THEME_CYCLE', () => {
  it('mirrors THEMES order exactly', () => {
    expect(THEME_CYCLE).toEqual(THEMES.map((t) => t.value));
  });

  it('offers exactly light, dark and system', () => {
    expect([...THEME_CYCLE].sort()).toEqual(['dark', 'light', 'system']);
  });
});

describe('normalizeTheme', () => {
  it('keeps supported themes', () => {
    expect(normalizeTheme('light')).toBe('light');
    expect(normalizeTheme('dark')).toBe('dark');
    expect(normalizeTheme('system')).toBe('system');
  });

  it('maps legacy palettes and junk to system', () => {
    expect(normalizeTheme('nord')).toBe('system');
    expect(normalizeTheme('sepia')).toBe('system');
    expect(normalizeTheme('toString')).toBe('system');
    expect(normalizeTheme(42)).toBe('system');
    expect(normalizeTheme(null)).toBe('system');
  });
});
