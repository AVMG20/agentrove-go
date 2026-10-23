import { describe, expect, it } from 'vitest';
import {
  USAGE_POLL_ACTIVE_MS,
  USAGE_POLL_IDLE_MS,
  describeUsageWindow,
  formatResetIn,
  nextResetMs,
  usagePollInterval,
  usageTone,
} from './claudeUsage';

const NOW = Date.parse('2026-10-22T12:00:00Z');

describe('claudeUsage', () => {
  it('polls every 2 minutes while working and hourly otherwise', () => {
    expect(usagePollInterval(true)).toBe(USAGE_POLL_ACTIVE_MS);
    expect(USAGE_POLL_ACTIVE_MS).toBe(120_000);
    expect(usagePollInterval(false)).toBe(USAGE_POLL_IDLE_MS);
    expect(USAGE_POLL_IDLE_MS).toBe(3_600_000);
  });

  it('formats reset countdowns compactly', () => {
    expect(formatResetIn(0)).toBe('now');
    expect(formatResetIn(30_000)).toBe('<1m');
    expect(formatResetIn(48 * 60_000)).toBe('48m');
    expect(formatResetIn((3 * 60 + 54) * 60_000)).toBe('3h 54m');
    expect(formatResetIn(2 * 60 * 60_000)).toBe('2h');
    expect(formatResetIn((6 * 24 + 7) * 60 * 60_000 + 59_000)).toBe('6d 7h');
  });

  it('maps usage to tones', () => {
    expect(usageTone(10)).toBe('normal');
    expect(usageTone(75)).toBe('warning');
    expect(usageTone(90)).toBe('danger');
  });

  it('picks the earliest reset still ahead', () => {
    const past = { used_percent: 1, resets_at: '2026-10-22T11:00:00Z' };
    const soon = { used_percent: 1, resets_at: '2026-10-22T13:00:00Z' };
    const later = { used_percent: 1, resets_at: '2026-10-25T13:00:00Z' };
    expect(nextResetMs([later, past, soon, null], NOW)).toBe(Date.parse(soon.resets_at));
    expect(nextResetMs([past, { used_percent: 1, resets_at: 'bogus' }], NOW)).toBeNull();
  });

  it('describes a window for the tooltip', () => {
    const window = { used_percent: 51.4, resets_at: '2026-10-22T12:48:00Z' };
    expect(describeUsageWindow('5-hour limit', window, NOW)).toMatch(
      /^5-hour limit: 51% used · resets in 48m \(/,
    );
    expect(describeUsageWindow('Weekly limit', { used_percent: 3, resets_at: null }, NOW)).toBe(
      'Weekly limit: 3% used',
    );
  });
});
