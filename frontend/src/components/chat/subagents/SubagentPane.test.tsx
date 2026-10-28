// @vitest-environment happy-dom

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useUIStore } from '@/store/uiStore';
import type { Subagent } from '@/types/chat.types';
import { SubagentPane } from './SubagentPane';

const h = vi.hoisted(() => ({
  list: [] as Subagent[],
  detail: undefined as Subagent | undefined,
}));
vi.mock('@/hooks/queries/useSubagentQueries', () => ({
  useChatSubagentsQuery: () => ({ data: h.list, isLoading: false }),
  useSubagentQuery: () => ({ data: h.detail, isError: false }),
}));
vi.mock('@/services/chatService', () => ({
  chatService: { stopBackgroundTask: () => new Promise<void>(() => {}) },
}));
vi.mock('@/components/ui/markdown/MarkDown', () => ({
  MarkDown: ({ content }: { content: string }) => <div>{content}</div>,
}));

function subagent(over: Partial<Subagent> = {}): Subagent {
  return {
    id: 'a1',
    chat_id: 'c1',
    description: 'Build compose editor fixes',
    subagent_type: 'general-purpose',
    background: true,
    status: 'running',
    tool_count: 2,
    last_activity: 'Read Compose.tsx',
    task_id: 'task1',
    started_at: 1_800_000_000 - 90,
    finished_at: null,
    ...over,
  };
}

beforeEach(() => {
  useUIStore.setState({
    openTabs: ['agent:primary', 'subagent'],
    visibleLayout: [['agent:primary', 'subagent']],
    subagentByChat: { c1: 'a1' },
  });
  h.list = [subagent(), subagent({ id: 'a2', description: 'Audit', status: 'completed' })];
  h.detail = subagent({
    prompt: 'Fix drag and drop in the compose window.',
    activity: [
      { kind: 'text', text: 'Looking at the drop handler', at: 1 },
      {
        kind: 'tool',
        id: 't1',
        name: 'Read',
        title: 'Read Compose.tsx',
        status: 'completed',
        at: 2,
      },
    ],
  });
});

afterEach(cleanup);

describe('SubagentPane', () => {
  it('shows the prompt and what the subagent is doing', () => {
    render(<SubagentPane chatId="c1" tileId="subagent" isVisible />);
    expect(screen.getByText('Build compose editor fixes')).toBeTruthy();
    expect(screen.getByText(/Running in background/)).toBeTruthy();
    expect(screen.getByText('Fix drag and drop in the compose window.')).toBeTruthy();
    expect(screen.getByText('Looking at the drop handler')).toBeTruthy();
    expect(screen.getByText('Read Compose.tsx')).toBeTruthy();
    expect(screen.getByText('Working…')).toBeTruthy();
    expect(screen.getByLabelText('Stop subagent')).toBeTruthy();
  });

  it('shows the result once finished', () => {
    h.list = [
      subagent({ status: 'completed', result: 'All four fixed', finished_at: 1_800_000_000 }),
    ];
    render(<SubagentPane chatId="c1" tileId="subagent" isVisible />);
    expect(screen.getByText('All four fixed')).toBeTruthy();
    expect(screen.queryByText('Working…')).toBeNull();
    expect(screen.queryByLabelText('Stop subagent')).toBeNull();
  });

  it("doesn't carry a pending stop over to the next subagent", () => {
    h.list = [subagent(), subagent({ id: 'a2', description: 'Audit', task_id: 'task2' })];
    render(<SubagentPane chatId="c1" tileId="subagent" isVisible />);
    fireEvent.click(screen.getByLabelText('Stop subagent'));
    expect(screen.getByLabelText<HTMLButtonElement>('Stop subagent').disabled).toBe(true);
    act(() => useUIStore.setState({ subagentByChat: { c1: 'a2' } }));
    expect(screen.getByText('Audit')).toBeTruthy();
    expect(screen.getByLabelText<HTMLButtonElement>('Stop subagent').disabled).toBe(false);
  });

  it('goes back to the list and closes', () => {
    render(<SubagentPane chatId="c1" tileId="subagent" isVisible />);
    fireEvent.click(screen.getByLabelText('All subagents'));
    expect(screen.getByText('Audit')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Close subagent view'));
    expect(useUIStore.getState().visibleLayout).toEqual([['agent:primary']]);
  });
});
