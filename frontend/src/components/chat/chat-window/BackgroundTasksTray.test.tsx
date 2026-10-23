// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import type { BackgroundTask } from '@/types/chat.types';
import { BackgroundTasksTray } from './BackgroundTasksTray';

const stopBackgroundTask = vi.fn<(chatId: string, taskId: string) => Promise<void>>();
vi.mock('@/services/chatService', () => ({
  chatService: {
    stopBackgroundTask: (chatId: string, taskId: string) => stopBackgroundTask(chatId, taskId),
  },
}));

const NOW = 1_800_000_000_000;

function task(over: Partial<BackgroundTask> = {}): BackgroundTask {
  return {
    id: 'b1',
    kind: 'shell',
    title: './gradlew build',
    tool_id: 'tool-1',
    started_at: NOW / 1000 - 134,
    stoppable: true,
    ...over,
  };
}

describe('BackgroundTasksTray', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    stopBackgroundTask.mockReset().mockResolvedValue(undefined);
    useBackgroundTasksStore.setState({ tasksByChat: {} });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('renders nothing while the chat has no background work', () => {
    const { container } = render(<BackgroundTasksTray chatId="c1" onStopTurn={vi.fn()} />);
    expect(container.firstChild).toBeNull();
  });

  it('lists running work with a ticking elapsed time', () => {
    useBackgroundTasksStore
      .getState()
      .setChatTasks('c1', [
        task(),
        task({ id: 'a1', kind: 'agent', title: 'Review the diff', stoppable: false }),
      ]);
    render(<BackgroundTasksTray chatId="c1" onStopTurn={vi.fn()} />);

    expect(screen.getByText('1 command · 1 subagent')).toBeTruthy();
    expect(screen.getByText('./gradlew build')).toBeTruthy();
    expect(screen.getAllByText('2m 14s')).toHaveLength(2);

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getAllByText('2m 16s')).toHaveLength(2);
  });

  it('stops a shell itself but a subagent through its turn', () => {
    const onStopTurn = vi.fn();
    useBackgroundTasksStore
      .getState()
      .setChatTasks('c1', [
        task(),
        task({ id: 'a1', kind: 'agent', title: 'Review the diff', stoppable: false }),
      ]);
    render(<BackgroundTasksTray chatId="c1" onStopTurn={onStopTurn} />);

    fireEvent.click(screen.getByLabelText('Stop ./gradlew build'));
    expect(stopBackgroundTask).toHaveBeenCalledWith('c1', 'b1');
    expect(onStopTurn).not.toHaveBeenCalled();

    fireEvent.click(screen.getByLabelText('Stop Review the diff'));
    expect(onStopTurn).toHaveBeenCalledTimes(1);
  });

  it('collapses to the summary line', () => {
    useBackgroundTasksStore.getState().setChatTasks('c1', [task()]);
    render(<BackgroundTasksTray chatId="c1" onStopTurn={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { expanded: true }));
    expect(screen.queryByText('./gradlew build')).toBeNull();
    expect(screen.getByText('1 command')).toBeTruthy();
  });
});
