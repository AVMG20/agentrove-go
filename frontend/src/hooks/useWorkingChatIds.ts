import { useMemo } from 'react';
import { useStreamStore } from '@/store/streamStore';
import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';

// Chats with a turn streaming or work still running in the background — a
// backgrounded build keeps its chat "working" after the turn has ended.
export function useWorkingChatIds(): Set<string> {
  const activeStreamMetadata = useStreamStore((state) => state.activeStreamMetadata);
  const tasksByChat = useBackgroundTasksStore((state) => state.tasksByChat);
  return useMemo(
    () =>
      new Set([...activeStreamMetadata.map((meta) => meta.chatId), ...Object.keys(tasksByChat)]),
    [activeStreamMetadata, tasksByChat],
  );
}

export function useIsChatWorking(chatId: string): boolean {
  const streaming = useStreamStore((state) =>
    state.activeStreamMetadata.some((meta) => meta.chatId === chatId),
  );
  const backgrounded = useBackgroundTasksStore((state) => chatId in state.tasksByChat);
  return streaming || backgrounded;
}
