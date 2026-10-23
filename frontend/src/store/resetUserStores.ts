import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import { useChannelStore } from '@/store/channelStore';
import { useChatStore } from '@/store/chatStore';
import { useElicitationStore } from '@/store/elicitationStore';
import { useChannelQueueStore, useMessageQueueStore } from '@/store/messageQueueStore';
import { usePermissionStore } from '@/store/permissionStore';
import { useStreamStore } from '@/store/streamStore';

// In-memory stores holding one account's live data. An SPA sign-out doesn't reload, so
// without this the next sign-in inherits the previous account's streams (which keep the
// shared SSE connection open on the old token), queues and pending prompts.
export function resetUserScopedStores(): void {
  // Deactivate first: the SSE manager tears down once no active stream remains.
  for (const stream of useStreamStore.getState().activeStreams.values()) {
    stream.isActive = false;
    stream.callbacks = undefined;
  }
  useStreamStore.setState(useStreamStore.getInitialState(), true);
  useMessageQueueStore.setState(useMessageQueueStore.getInitialState(), true);
  useChannelQueueStore.setState(useChannelQueueStore.getInitialState(), true);
  usePermissionStore.setState(usePermissionStore.getInitialState(), true);
  useElicitationStore.setState(useElicitationStore.getInitialState(), true);
  useBackgroundTasksStore.setState(useBackgroundTasksStore.getInitialState(), true);
  useChannelStore.setState(useChannelStore.getInitialState(), true);
  useChatStore.setState(useChatStore.getInitialState(), true);
}
