export interface ClaudeUsageWindow {
  used_percent: number;
  resets_at: string | null;
}

// GET /usage/claude — 'unavailable' when the Claude proxy has no usage endpoint.
export interface ClaudeUsage {
  status: 'ok' | 'error' | 'unavailable';
  provider: string | null;
  session: ClaudeUsageWindow | null;
  weekly: ClaudeUsageWindow | null;
  error: string | null;
  fetched_at: string;
}
