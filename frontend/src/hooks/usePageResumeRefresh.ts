import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useStreamStore } from '@/store/streamStore';
import { PAGE_RESUME_IDLE_MS, isOffline, subscribePageResume } from '@/utils/pageResume';

// refetchOnWindowFocus is off and staleTime is long, so open threads go stale across
// sleep / long tab switches. Chat, list and workspace caches are refreshed by the
// lifecycle feeds' onResync (forced on the same resume), so only messages remain here.
export function usePageResumeRefresh(options?: { enabled?: boolean }) {
  const enabled = options?.enabled ?? true;
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!enabled) return;
    return subscribePageResume(({ idleMs }) => {
      if (idleMs < PAGE_RESUME_IDLE_MS || isOffline()) return;
      const { getStreamByChat } = useStreamStore.getState();
      void queryClient.invalidateQueries({
        queryKey: ['messages'],
        refetchType: 'active',
        // Live streams catch up via SSE replay; a mid-stream refetch would clobber cache patches.
        predicate: (query) => {
          const chatId = query.queryKey[1];
          return typeof chatId !== 'string' || !getStreamByChat(chatId);
        },
      });
    });
  }, [enabled, queryClient]);
}
