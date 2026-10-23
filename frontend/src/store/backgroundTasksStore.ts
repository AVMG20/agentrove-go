import { create } from 'zustand';
import type { BackgroundTask, ChatBackgroundTasks } from '@/types/chat.types';

const EMPTY_TASKS: BackgroundTask[] = [];

interface BackgroundTasksState {
  // Only chats with running work have an entry.
  tasksByChat: Record<string, BackgroundTask[]>;
  setChatTasks: (chatId: string, tasks: BackgroundTask[]) => void;
  // A snapshot replaces everything reported before.
  replaceSnapshot: (entries: ChatBackgroundTasks[]) => void;
}

export const useBackgroundTasksStore = create<BackgroundTasksState>((set) => ({
  tasksByChat: {},

  setChatTasks: (chatId, tasks) => {
    set((state) => {
      if (tasks.length === 0 && !(chatId in state.tasksByChat)) return state;
      const next = { ...state.tasksByChat };
      if (tasks.length === 0) delete next[chatId];
      else next[chatId] = tasks;
      return { tasksByChat: next };
    });
  },

  replaceSnapshot: (entries) => {
    const next: Record<string, BackgroundTask[]> = {};
    for (const entry of entries) {
      if (entry.tasks.length > 0) next[entry.chat_id] = entry.tasks;
    }
    set({ tasksByChat: next });
  },
}));

export function useChatBackgroundTasks(chatId: string | undefined): BackgroundTask[] {
  return useBackgroundTasksStore((state) =>
    chatId ? (state.tasksByChat[chatId] ?? EMPTY_TASKS) : EMPTY_TASKS,
  );
}
