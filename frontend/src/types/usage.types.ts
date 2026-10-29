// One rate-limit window of an account's plan: 5-hour, weekly, monthly, ...
export interface UsageWindow {
  used_percent: number;
  resets_at: string | null;
  window_seconds: number;
}

// One device sharing a proxied plan: its share of this week's plan usage and
// its color slot (1-based).
export interface UsageDevice {
  name: string;
  color: number;
  used_percent: number;
  blocked: boolean;
}

// GET /usage — 'unavailable' when the account has no usage to show (an API
// key, Antigravity, or a gateway without a usage endpoint).
export interface AccountUsage {
  status: 'ok' | 'error' | 'unavailable';
  provider: string | null;
  // Shortest window first.
  windows: UsageWindow[];
  // Set when a proxy splits the plan between devices.
  devices?: UsageDevice[];
  error: string | null;
  fetched_at: string;
}
