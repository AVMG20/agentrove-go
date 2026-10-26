// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import { useUIStore } from '@/store/uiStore';
import type { BackgroundTask, Subagent } from '@/types/chat.types';
import { BackgroundTasksTray } from './BackgroundTasksTray';

const stopBackgroundTask = vi.fn<(chatId: string, taskId: string) => Promise<void>>();
vi.mock('@/services/chatService', () => ({
  chatService: {
    stopBackgroundTask: (chatId: string, taskId: string) => stopBackgroundTask(chatId, taskId),
  },
}));

const subagentsByChat: Record<string, Subagent[]> = {};
vi.mock('@/hooks/queries/useSubagentQueries', () => ({
  useChatSubagentsQuery: (chatId: string) => ({ data: subagentsByChat[chatId] }),
}));

const NOW = 1_800_000_000_000;

function subagent(over: Partial<Subagent> = {}): Subagent {
  return {
    id: 'a1',
    chat_id: 'c1',
    description: 'Review the diff',
    subagent_type: 'general-purpose',
    background: true,
    status: 'running',
    tool_count: 3,
    last_activity: 'Read main.go',
    started_at: NOW / 1000 - 60,
    finished_at: null,
    ...over,
  };
}

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
    for (const key of Object.keys(subagentsByChat)) delete subagentsByChat[key];
    useUIStore.setState({
      openTabs: ['agent:primary'],
      visibleLayout: [['agent:primary']],
      subagentByChat: {},
    });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it('renders nothing while the chat has no background work', () => {
    const { container } = render(<BackgroundTasksTray chatId="c1" />);
    expect(container.firstChild).toBeNull();
  });

  it('lists running commands with a ticking elapsed time', () => {
    useBackgroundTasksStore
      .getState()
      .setChatTasks('c1', [task(), task({ id: 'b2', title: 'npm run dev' })]);
    render(<BackgroundTasksTray chatId="c1" />);

    expect(screen.getByText('2 commands')).toBeTruthy();
    expect(screen.getByText('./gradlew build')).toBeTruthy();
    expect(screen.getAllByText('2m 14s')).toHaveLength(2);

    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(screen.getAllByText('2m 16s')).toHaveLength(2);
  });

  it('sums running subagents up in one row that opens the subagent', () => {
    subagentsByChat.c1 = [subagent(), subagent({ id: 'a0', status: 'completed' })];
    useBackgroundTasksStore
      .getState()
      .setChatTasks('c1', [task({ id: 't1', kind: 'agent', title: 'Review the diff' })]);
    render(<BackgroundTasksTray chatId="c1" />);

    expect(screen.getByText('1 subagent')).toBeTruthy();
    expect(screen.getByText('1m 00s')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Show subagent Review the diff'));
    expect(useUIStore.getState().subagentByChat).toEqual({ c1: 'a1' });
  });

  it('opens the list when several subagents run', () => {
    subagentsByChat.c1 = [subagent(), subagent({ id: 'a2', description: 'Audit' })];
    render(<BackgroundTasksTray chatId="c1" />);

    expect(screen.getByText('2 subagents', { selector: 'span[class*="row-title"]' })).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Show 2 subagents'));
    expect(useUIStore.getState().subagentByChat).toEqual({});
    expect(useUIStore.getState().openTabs).toContain('subagent');
  });

  it('stops a command', () => {
    useBackgroundTasksStore
      .getState()
      .setChatTasks('c1', [task(), task({ id: 'a1', kind: 'agent', title: 'Review the diff' })]);
    render(<BackgroundTasksTray chatId="c1" />);

    expect(screen.getByText('1 command')).toBeTruthy();
    expect(screen.queryByText('Review the diff')).toBeNull();
    fireEvent.click(screen.getByLabelText('Stop ./gradlew build'));
    expect(stopBackgroundTask).toHaveBeenCalledWith('c1', 'b1');
  });

  it('collapses to the summary line', () => {
    useBackgroundTasksStore.getState().setChatTasks('c1', [task()]);
    render(<BackgroundTasksTray chatId="c1" />);
    fireEvent.click(screen.getByRole('button', { expanded: true }));
    expect(screen.queryByText('./gradlew build')).toBeNull();
    expect(screen.getByText('1 command')).toBeTruthy();
  });
});
