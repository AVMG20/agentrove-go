import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import { useChatStore } from '@/store/chatStore';
import { useComposerDraftStore } from '@/store/composerDraftStore';
import { useElicitationStore } from '@/store/elicitationStore';
import { useMessageQueueStore } from '@/store/messageQueueStore';
import { usePermissionStore } from '@/store/permissionStore';
import { useProfileStore } from '@/store/profileStore';
import { useStreamStore } from '@/store/streamStore';

// Stores holding one account's live data (all in memory but composer drafts). An SPA sign-out doesn't reload, so
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
  usePermissionStore.setState(usePermissionStore.getInitialState(), true);
  useElicitationStore.setState(useElicitationStore.getInitialState(), true);
  useBackgroundTasksStore.setState(useBackgroundTasksStore.getInitialState(), true);
  useChatStore.setState(useChatStore.getInitialState(), true);
  // Persisted, so the next account on this browser doesn't see unsent text.
  useComposerDraftStore.setState(useComposerDraftStore.getInitialState(), true);
  // Profile ids are per account.
  useProfileStore.setState(useProfileStore.getInitialState(), true);
}
