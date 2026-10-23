import { beforeEach, describe, expect, it } from 'vitest';
import type { BackgroundTask } from '@/types/chat.types';
import { useBackgroundTasksStore } from './backgroundTasksStore';

const shell: BackgroundTask = {
  id: 'b1',
  kind: 'shell',
  title: 'npm run dev',
  tool_id: 't1',
  started_at: 1,
  stoppable: true,
};

beforeEach(() => {
  useBackgroundTasksStore.setState({ tasksByChat: {} });
});

describe('backgroundTasksStore', () => {
  it('keeps entries only for chats with running work', () => {
    const { setChatTasks } = useBackgroundTasksStore.getState();
    setChatTasks('c1', [shell]);
    expect(useBackgroundTasksStore.getState().tasksByChat).toEqual({ c1: [shell] });
    setChatTasks('c1', []);
    expect(useBackgroundTasksStore.getState().tasksByChat).toEqual({});
  });

  it('replaces every chat with the snapshot', () => {
    const { setChatTasks, replaceSnapshot } = useBackgroundTasksStore.getState();
    setChatTasks('old', [shell]);
    replaceSnapshot([
      { chat_id: 'new', tasks: [shell] },
      { chat_id: 'idle', tasks: [] },
    ]);
    expect(useBackgroundTasksStore.getState().tasksByChat).toEqual({ new: [shell] });
  });
});
