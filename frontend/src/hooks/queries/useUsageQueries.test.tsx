// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useStreamStore } from '@/store/streamStore';
import type { ClaudeUsage } from '@/types/usage.types';
import { useClaudeUsage } from './useUsageQueries';

const getClaudeUsage = vi.fn<(refresh?: boolean) => Promise<ClaudeUsage>>();
vi.mock('@/services/usageService', () => ({
  usageService: { getClaudeUsage: (refresh?: boolean) => getClaudeUsage(refresh) },
}));

const MINUTE = 60_000;

function usage(status: ClaudeUsage['status'], usedPercent = 10): ClaudeUsage {
  return {
    status,
    provider: status === 'unavailable' ? null : 'claude.bitsec.dev',
    session:
      status === 'ok'
        ? {
            used_percent: usedPercent,
            resets_at: new Date(Date.now() + 10 * 24 * 60 * MINUTE).toISOString(),
          }
        : null,
    weekly: null,
    error: null,
    fetched_at: new Date().toISOString(),
  };
}

function setup() {
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(() => useClaudeUsage(), { wrapper });
}

const flush = () => act(async () => vi.advanceTimersByTimeAsync(0));

function setWorking(working: boolean) {
  act(() => {
    useStreamStore.setState({
      activeStreamMetadata: working ? [{ chatId: 'c1', messageId: 'm1', startTime: 0 }] : [],
    });
  });
}

describe('useClaudeUsage', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    getClaudeUsage.mockReset();
    useStreamStore.setState({ activeStreamMetadata: [] });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('polls hourly when idle and every 2 minutes while a turn runs', async () => {
    getClaudeUsage.mockResolvedValue(usage('ok'));
    setup();
    await flush();
    expect(getClaudeUsage).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTimeAsync(30 * MINUTE));
    expect(getClaudeUsage).toHaveBeenCalledTimes(1);

    setWorking(true);
    await act(async () => vi.advanceTimersByTimeAsync(2 * MINUTE + 100));
    expect(getClaudeUsage).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTimeAsync(2 * MINUTE));
    expect(getClaudeUsage).toHaveBeenCalledTimes(3);
  });

  it('fetches fresh numbers shortly after a turn finishes', async () => {
    getClaudeUsage.mockResolvedValue(usage('ok'));
    setWorking(true);
    const { result } = setup();
    await flush();

    getClaudeUsage.mockResolvedValue(usage('ok', 42));
    setWorking(false);
    await act(async () => vi.advanceTimersByTimeAsync(16_000));

    expect(getClaudeUsage).toHaveBeenLastCalledWith(true);
    expect(result.current.usage?.session?.used_percent).toBe(42);
  });

  it('stops polling when the proxy has no usage endpoint', async () => {
    getClaudeUsage.mockResolvedValue(usage('unavailable'));
    setWorking(true);
    setup();
    await flush();
    await act(async () => vi.advanceTimersByTimeAsync(10 * MINUTE));
    expect(getClaudeUsage).toHaveBeenCalledTimes(1);
  });

  it('manual refresh bypasses the cache and updates the numbers', async () => {
    getClaudeUsage.mockResolvedValue(usage('ok'));
    const { result } = setup();
    await flush();

    getClaudeUsage.mockResolvedValue(usage('ok', 77));
    await act(async () => {
      await result.current.refresh();
    });

    expect(getClaudeUsage).toHaveBeenLastCalledWith(true);
    expect(result.current.usage?.session?.used_percent).toBe(77);
    expect(result.current.isRefreshing).toBe(false);
  });
});
