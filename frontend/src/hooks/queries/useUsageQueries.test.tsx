// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useStreamStore } from '@/store/streamStore';
import type { AccountUsage } from '@/types/usage.types';
import { useAccountUsage } from './useUsageQueries';

const getUsage = vi.fn<(refresh?: boolean, profileId?: string) => Promise<AccountUsage>>();
vi.mock('@/services/usageService', () => ({
  usageService: {
    getUsage: (refresh?: boolean, profileId?: string) => getUsage(refresh, profileId),
  },
}));

const MINUTE = 60_000;

function usage(status: AccountUsage['status'], usedPercent = 10): AccountUsage {
  return {
    status,
    provider: status === 'unavailable' ? null : 'claude.bitsec.dev',
    windows:
      status === 'ok'
        ? [
            {
              used_percent: usedPercent,
              resets_at: new Date(Date.now() + 10 * 24 * 60 * MINUTE).toISOString(),
              window_seconds: 5 * 60 * 60,
            },
          ]
        : [],
    error: null,
    fetched_at: new Date().toISOString(),
  };
}

function setup(profileId?: string) {
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(() => useAccountUsage(profileId), { wrapper });
}

const flush = () => act(async () => vi.advanceTimersByTimeAsync(0));

function setWorking(working: boolean) {
  act(() => {
    useStreamStore.setState({
      activeStreamMetadata: working ? [{ chatId: 'c1', messageId: 'm1', startTime: 0 }] : [],
    });
  });
}

describe('useAccountUsage', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    getUsage.mockReset();
    useStreamStore.setState({ activeStreamMetadata: [] });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('polls hourly when idle and every 2 minutes while a turn runs', async () => {
    getUsage.mockResolvedValue(usage('ok'));
    setup();
    await flush();
    expect(getUsage).toHaveBeenCalledTimes(1);

    await act(async () => vi.advanceTimersByTimeAsync(30 * MINUTE));
    expect(getUsage).toHaveBeenCalledTimes(1);

    setWorking(true);
    await act(async () => vi.advanceTimersByTimeAsync(2 * MINUTE + 100));
    expect(getUsage).toHaveBeenCalledTimes(2);
    await act(async () => vi.advanceTimersByTimeAsync(2 * MINUTE));
    expect(getUsage).toHaveBeenCalledTimes(3);
  });

  it('fetches fresh numbers shortly after a turn finishes', async () => {
    getUsage.mockResolvedValue(usage('ok'));
    setWorking(true);
    const { result } = setup();
    await flush();

    getUsage.mockResolvedValue(usage('ok', 42));
    setWorking(false);
    await act(async () => vi.advanceTimersByTimeAsync(16_000));

    expect(getUsage).toHaveBeenLastCalledWith(true, undefined);
    expect(result.current.usage?.windows[0]?.used_percent).toBe(42);
  });

  it('stops polling when the proxy has no usage endpoint', async () => {
    getUsage.mockResolvedValue(usage('unavailable'));
    setWorking(true);
    setup();
    await flush();
    await act(async () => vi.advanceTimersByTimeAsync(10 * MINUTE));
    expect(getUsage).toHaveBeenCalledTimes(1);
  });

  it('manual refresh bypasses the cache and updates the numbers', async () => {
    getUsage.mockResolvedValue(usage('ok'));
    const { result } = setup();
    await flush();

    getUsage.mockResolvedValue(usage('ok', 77));
    await act(async () => {
      await result.current.refresh();
    });

    expect(getUsage).toHaveBeenLastCalledWith(true, undefined);
    expect(result.current.usage?.windows[0]?.used_percent).toBe(77);
    expect(result.current.isRefreshing).toBe(false);
  });

  it('fetches the given account and the default one without an id', async () => {
    getUsage.mockResolvedValue(usage('ok'));
    setup('p-work');
    await flush();
    expect(getUsage).toHaveBeenLastCalledWith(false, 'p-work');

    setup();
    await flush();
    expect(getUsage).toHaveBeenLastCalledWith(false, undefined);
  });
});
