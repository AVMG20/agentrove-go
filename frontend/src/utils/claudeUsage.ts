import type { ClaudeUsageWindow } from '@/types/usage.types';

// Poll often while an agent turn is burning usage, rarely otherwise.
export const USAGE_POLL_ACTIVE_MS = 2 * 60_000;
export const USAGE_POLL_IDLE_MS = 60 * 60_000;
// Refetch a little after a window resets so the gateway has rolled it over.
export const USAGE_RESET_GRACE_MS = 15_000;
// Past the server's manual-refresh throttle and the gateway's metering lag.
export const USAGE_AFTER_TURN_DELAY_MS = 15_000;

export type UsageTone = 'normal' | 'warning' | 'danger';

export function usagePollInterval(working: boolean): number {
  return working ? USAGE_POLL_ACTIVE_MS : USAGE_POLL_IDLE_MS;
}

export function usageTone(usedPercent: number): UsageTone {
  if (usedPercent >= 90) return 'danger';
  if (usedPercent >= 75) return 'warning';
  return 'normal';
}

export function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(100, Math.max(0, value));
}

export function resetAtMs(window: ClaudeUsageWindow | null): number | null {
  if (!window?.resets_at) return null;
  const ms = Date.parse(window.resets_at);
  return Number.isNaN(ms) ? null : ms;
}

// "48m", "3h 54m", "6d 7h"; floors so it never overstates the wait.
export function formatResetIn(ms: number): string {
  if (ms <= 0) return 'now';
  const totalMins = Math.floor(ms / 60_000);
  if (totalMins < 1) return '<1m';
  if (totalMins < 60) return `${totalMins}m`;
  const hours = Math.floor(totalMins / 60);
  const mins = totalMins % 60;
  if (hours >= 24) {
    const days = Math.floor(hours / 24);
    const remHours = hours % 24;
    return remHours > 0 ? `${days}d ${remHours}h` : `${days}d`;
  }
  return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`;
}

export function describeUsageWindow(
  label: string,
  window: ClaudeUsageWindow,
  now: number = Date.now(),
): string {
  const used = `${Math.round(clampPercent(window.used_percent))}% used`;
  const resetAt = resetAtMs(window);
  if (resetAt == null) return `${label}: ${used}`;
  if (resetAt <= now) return `${label}: ${used} · resets now`;
  const when = new Date(resetAt).toLocaleString(undefined, {
    weekday: resetAt - now > 24 * 60 * 60_000 ? 'short' : undefined,
    hour: '2-digit',
    minute: '2-digit',
  });
  return `${label}: ${used} · resets in ${formatResetIn(resetAt - now)} (${when})`;
}

// Earliest reset still ahead, so the meter can refetch once it rolls over.
export function nextResetMs(
  windows: Array<ClaudeUsageWindow | null>,
  now: number = Date.now(),
): number | null {
  let next: number | null = null;
  for (const window of windows) {
    const at = resetAtMs(window);
    if (at != null && at > now && (next == null || at < next)) next = at;
  }
  return next;
}
