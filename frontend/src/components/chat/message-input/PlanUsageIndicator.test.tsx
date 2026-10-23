// @vitest-environment jsdom

import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ClaudeUsage } from '@/types/usage.types';

const h = vi.hoisted(() => ({ usage: undefined as ClaudeUsage | undefined }));

vi.mock('@/hooks/queries/useUsageQueries', () => ({
  useClaudeUsage: () => ({ usage: h.usage, isRefreshing: false, refresh: vi.fn() }),
}));

import { PlanUsageIndicator } from './PlanUsageIndicator';

const NOW = Date.parse('2026-10-23T12:00:00Z');
const inMinutes = (minutes: number) => new Date(NOW + minutes * 60_000 + 30_000).toISOString();

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('PlanUsageIndicator', () => {
  it('shows both windows with time to reset on one line', () => {
    h.usage = {
      status: 'ok',
      provider: null,
      session: { used_percent: 39.4, resets_at: inMinutes(4 * 60 + 18) },
      weekly: { used_percent: 6, resets_at: inMinutes((6 * 24 + 19) * 60) },
      error: null,
      fetched_at: new Date(NOW).toISOString(),
    };
    render(<PlanUsageIndicator />);
    expect(screen.getByText('39% 4h 18m · 6% 6d 19h')).toBeTruthy();
  });

  it('renders nothing when the proxy has no usage endpoint', () => {
    h.usage = {
      status: 'unavailable',
      provider: null,
      session: null,
      weekly: null,
      error: null,
      fetched_at: new Date(NOW).toISOString(),
    };
    const { container } = render(<PlanUsageIndicator />);
    expect(container.firstChild).toBeNull();
  });
});
