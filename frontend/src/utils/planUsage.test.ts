import { describe, expect, it } from 'vitest';
import {
  USAGE_POLL_ACTIVE_MS,
  USAGE_POLL_IDLE_MS,
  describeUsageWindow,
  formatResetIn,
  nextResetMs,
  usagePollInterval,
  usageTone,
  windowLabels,
} from './planUsage';

const NOW = Date.parse('2026-10-22T12:00:00Z');
const HOUR = 3600;
const win = (used_percent: number, resets_at: string | null, window_seconds = 5 * HOUR) => ({
  used_percent,
  resets_at,
  window_seconds,
});

describe('planUsage', () => {
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
    const past = win(1, '2026-10-22T11:00:00Z');
    const soon = win(1, '2026-10-22T13:00:00Z');
    const later = win(1, '2026-10-25T13:00:00Z', 168 * HOUR);
    expect(nextResetMs([later, past, soon, null], NOW)).toBe(Date.parse('2026-10-22T13:00:00Z'));
    expect(nextResetMs([past, win(1, 'bogus')], NOW)).toBeNull();
  });

  it('describes a window for the tooltip', () => {
    const window = win(51.4, '2026-10-22T12:48:00Z');
    expect(describeUsageWindow('5-hour limit', window, NOW)).toMatch(
      /^5-hour limit: 51% used · resets in 48m \(/,
    );
    expect(describeUsageWindow('Weekly limit', win(3, null, 168 * HOUR), NOW)).toBe(
      'Weekly limit: 3% used',
    );
  });

  it('names windows by their length', () => {
    expect(windowLabels(5 * HOUR)).toEqual({ short: '5h', long: '5-hour limit' });
    expect(windowLabels(604800)).toEqual({ short: 'wk', long: 'Weekly limit' });
    expect(windowLabels(30 * 24 * HOUR)).toEqual({ short: 'mo', long: 'Monthly limit' });
    expect(windowLabels(2 * 24 * HOUR)).toEqual({ short: '2d', long: '2-day limit' });
    expect(windowLabels(HOUR)).toEqual({ short: '1h', long: '1-hour limit' });
    expect(windowLabels(60)).toEqual({ short: 'Limit', long: 'Usage limit' });
  });
});
