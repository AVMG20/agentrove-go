// One rate-limit window of an account's plan: 5-hour, weekly, monthly, ...
export interface UsageWindow {
  used_percent: number;
  resets_at: string | null;
  window_seconds: number;
}

// GET /usage — 'unavailable' when the account has no usage to show (an API
// key, Antigravity, or a gateway without a usage endpoint).
export interface AccountUsage {
  status: 'ok' | 'error' | 'unavailable';
  provider: string | null;
  // Shortest window first.
  windows: UsageWindow[];
  error: string | null;
  fetched_at: string;
}
