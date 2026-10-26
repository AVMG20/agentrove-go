import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { usageService } from '@/services/usageService';
import { useStreamStore } from '@/store/streamStore';
import type { AccountUsage } from '@/types/usage.types';
import {
  USAGE_AFTER_TURN_DELAY_MS,
  USAGE_POLL_ACTIVE_MS,
  USAGE_RESET_GRACE_MS,
  nextResetMs,
  usagePollInterval,
} from '@/utils/planUsage';

// Per profile, shared across every mounted meter so split panes trigger one fetch.
const freshInFlight = new Map<string, Promise<void>>();

const usageKey = (profileId: string) => [...queryKeys.usage, profileId] as const;

// Bypasses the server's short cache: used right after a turn or a reset, when
// the cached numbers are known to be behind.
function fetchFreshUsage(queryClient: QueryClient, profileId: string): Promise<void> {
  let pending = freshInFlight.get(profileId);
  if (!pending) {
    pending = usageService
      .getUsage(true, profileId || undefined)
      .then((fresh) => {
        queryClient.setQueryData(usageKey(profileId), fresh);
      })
      .catch(() => {
        // Keep the last numbers; the next poll retries.
      })
      .finally(() => {
        freshInFlight.delete(profileId);
      });
    freshInFlight.set(profileId, pending);
  }
  return pending;
}

// Plan usage of an account ('' or omitted: the default account). Off while
// !enabled (no account to show).
export function useAccountUsage(profileId = '', enabled = true) {
  const queryClient = useQueryClient();
  // Any running turn — this tab's, another tab's, or an automation's (the chat
  // events feed registers those too).
  const working = useStreamStore((state) => state.activeStreamMetadata.length > 0);
  const interval = usagePollInterval(working);

  const query = useQuery<AccountUsage>({
    queryKey: usageKey(profileId),
    queryFn: () => usageService.getUsage(false, profileId || undefined),
    enabled,
    // No usage endpoint for this proxy: stop polling until settings or the
    // profile change (saving either invalidates this query).
    refetchInterval: (q) => (q.state.data?.status === 'unavailable' ? false : interval),
    // Catch up after the window was hidden, when interval ticks are skipped.
    staleTime: USAGE_POLL_ACTIVE_MS,
    refetchOnWindowFocus: true,
    retry: false,
  });

  // A turn just finished: pick up what it spent instead of waiting an hour.
  // Delayed so the gateway has metered the turn's last request.
  const wasWorking = useRef(working);
  useEffect(() => {
    const finished = wasWorking.current && !working;
    wasWorking.current = working;
    if (!finished || !enabled) return;
    const timer = setTimeout(
      () => void fetchFreshUsage(queryClient, profileId),
      USAGE_AFTER_TURN_DELAY_MS,
    );
    return () => clearTimeout(timer);
  }, [working, queryClient, profileId, enabled]);

  const data = query.data;
  const nextReset = data?.status === 'ok' ? nextResetMs(data.windows) : null;
  useEffect(() => {
    if (nextReset == null) return;
    const delay = nextReset - Date.now() + USAGE_RESET_GRACE_MS;
    // setTimeout overflows past ~24.8 days; the poll covers anything that far out.
    if (delay > 2 ** 31 - 1) return;
    const timer = setTimeout(
      () => void fetchFreshUsage(queryClient, profileId),
      Math.max(0, delay),
    );
    return () => clearTimeout(timer);
  }, [nextReset, queryClient, profileId]);

  const [isRefreshing, setIsRefreshing] = useState(false);
  const refresh = useCallback(async () => {
    setIsRefreshing(true);
    try {
      await fetchFreshUsage(queryClient, profileId);
    } finally {
      setIsRefreshing(false);
    }
  }, [queryClient, profileId]);

  return { usage: data, isRefreshing: isRefreshing || query.isFetching, refresh };
}
