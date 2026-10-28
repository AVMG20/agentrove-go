import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { chatService } from '@/services/chatService';
import { applyCreatedChat, patchChatInCache } from '@/hooks/queries/useChatQueries';
import { applySubagentEvent } from '@/hooks/queries/useSubagentQueries';
import { useStreamStore } from '@/store/streamStore';
import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import { useMessageQueueStore } from '@/store/messageQueueStore';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { subscribeChatEventsFeed } from '@/utils/chatEventsFeed';
import { resyncActiveStreams } from '@/utils/activeStreams';
import { logger } from '@/utils/logger';
import { retryFailedQueuedMessages } from '@/utils/queuedMessageFailure';
import type { ChatEvent } from '@/types/chat.types';

// Per-user chat lifecycle SSE: out-of-band creates/turns (MCP, agents) surface live.
export function useChatEvents(options?: { enabled?: boolean }) {
  const enabled = options?.enabled ?? true;
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!enabled) return;

    // Guards in-flight resync snapshots across teardown (logout flips enabled) —
    // a late response must not register ghost stream indicators.
    let cancelled = false;

    // Background shells live on between turns, so the tray needs the current
    // list on load and after a gap, not just the changes.
    const syncBackgroundTasks = () => {
      chatService
        .getBackgroundTasks()
        .then((entries) => {
          if (cancelled) return;
          useBackgroundTasksStore.getState().replaceSnapshot(entries);
        })
        .catch((error) => logger.error('Background tasks sync failed', 'useChatEvents', error));
    };
    syncBackgroundTasks();

    // While this tab saves settings, a refetch could return an older folder
    // order than the one it just dropped: wait for its saves to land first.
    let settingsRetry: number | undefined;
    const refetchSettingsWhenIdle = () => {
      window.clearTimeout(settingsRetry);
      if (cancelled) return;
      if (queryClient.isMutating({ mutationKey: [queryKeys.settings] }) > 0) {
        settingsRetry = window.setTimeout(refetchSettingsWhenIdle, 500);
        return;
      }
      void queryClient.invalidateQueries({ queryKey: [queryKeys.settings] });
    };

    const onChatEvent = (event: MessageEvent) => {
      if (!event.data) return;
      let parsed: ChatEvent;
      try {
        parsed = JSON.parse(event.data) as ChatEvent;
      } catch (error) {
        logger.error('Malformed chat event', 'useChatEvents', error);
        return;
      }

      if (parsed.kind === 'chat_created') {
        void applyCreatedChat(queryClient, parsed.chat).catch((error) =>
          logger.error('Chat cache update failed', 'useChatEvents', error),
        );
        return;
      }

      if (parsed.kind === 'title_updated') {
        // Mid-stream title backfill — patch caches so sidebar/title rename without refetch.
        patchChatInCache(queryClient, parsed.chat_id, (chat) => ({
          ...chat,
          title: parsed.title,
        }));
        return;
      }

      if (parsed.kind === 'background_tasks') {
        useBackgroundTasksStore.getState().setChatTasks(parsed.chat_id, parsed.tasks);
        return;
      }

      if (parsed.kind === 'subagent') {
        applySubagentEvent(queryClient, parsed.chat_id, parsed.subagent);
        return;
      }

      if (parsed.kind === 'settings_updated') {
        // Saves from other tabs and devices (e.g. the sidebar folder order).
        refetchSettingsWhenIdle();
        return;
      }

      if (parsed.kind === 'workspaces_updated') {
        // A rename or folder icon/color; same hold-off as settings above.
        if (queryClient.isMutating({ mutationKey: queryKeys.workspaces }) === 0) {
          void queryClient.invalidateQueries({ queryKey: queryKeys.workspaces, exact: true });
        }
        return;
      }

      if (parsed.kind === 'stream_started') {
        // Server bumps updated_at — refetch so chats outside loaded pages surface.
        queryClient.invalidateQueries({ queryKey: [queryKeys.chats, 'infinite'] });
        queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
        // Drop cached detail so toolbar seeds model/thinking/persona from this turn.
        queryClient.invalidateQueries({ queryKey: queryKeys.chat(parsed.chat_id) });
        // Self-starts already have addStream; settled orphans pruned by useGlobalStream.
        useStreamStore.getState().addStreamMetadataIfAbsent({
          chatId: parsed.chat_id,
          messageId: parsed.message_id,
          startTime: Date.now(),
        });
        // A queued message started on an idle chat arrives as a plain stream_started,
        // not queue_processing — resync so its pending item drops out.
        const queueStore = useMessageQueueStore.getState();
        if (queueStore.getQueue(parsed.chat_id).length > 0) {
          void queueStore.fetchQueue(parsed.chat_id);
        }
      }
    };

    const unsubscribe = subscribeChatEventsFeed({
      createSource: () => chatService.createChatEventsSource(),
      onChatEvent,
      // Missed events while feed was down — refetch lists/caches and re-register streams.
      onResync: () => {
        void queryClient.invalidateQueries({ queryKey: [queryKeys.chats, 'infinite'] });
        void queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
        void queryClient.invalidateQueries({ queryKey: ['chat'] });
        void queryClient.invalidateQueries({ queryKey: queryKeys.subagentsAll });
        refetchSettingsWhenIdle();
        resyncActiveStreams(
          () => chatService.getActiveStreams(),
          () => cancelled,
          'useChatEvents',
        );
        syncBackgroundTasks();
        // Follow-ups that failed on the network while the feed was down.
        retryFailedQueuedMessages();
      },
      logContext: 'useChatEvents',
    });

    return () => {
      cancelled = true;
      window.clearTimeout(settingsRetry);
      unsubscribe();
    };
  }, [enabled, queryClient]);
}
