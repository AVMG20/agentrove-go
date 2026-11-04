// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Chat } from '@/types/chat.types';
import { useStreamStore } from '@/store/streamStore';
import { WorkingSessionsCard } from './WorkingSessionsCard';

const chat = (id: string, title: string): Chat => ({
  id,
  user_id: 'u',
  title,
  workspace_id: 'ws',
  sandbox_id: 's',
  no_project: false,
  created_at: '2026-11-04T00:00:00Z',
  updated_at: '2026-11-04T00:00:00Z',
  pinned_at: null,
  worktree_cwd: null,
  session_agent_kind: 'claude',
  unread: false,
  last_model_id: null,
  last_thinking_mode: null,
  last_persona_name: null,
  profile_id: '',
  provider: 'claude',
});

const chatsById = new Map([
  ['a', chat('a', 'Polynux split')],
  ['b', chat('b', 'SERP migration')],
]);
const badges = new Map([['ws', { name: 'nuxtpolymarket' }]]);
const none = new Set<string>();

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

const card = (working: Set<string>, completed: Set<string>, onSelect = vi.fn()) => (
  <WorkingSessionsCard
    chatsById={chatsById}
    workspaceBadgeById={badges}
    workingChatIds={working}
    completedChatIds={completed}
    blockedChatIds={none}
    onSelect={onSelect}
  />
);

afterEach(() => {
  cleanup();
  useStreamStore.setState({ lastToolTitleByChatId: new Map() });
});

describe('WorkingSessionsCard', () => {
  it('stays hidden with a single working session', () => {
    render(card(new Set(['a']), none), { wrapper });
    expect(screen.queryByLabelText('Working sessions')).toBeNull();
  });

  it('lists two working sessions with their current tool', () => {
    useStreamStore.setState({ lastToolTitleByChatId: new Map([['a', 'TaskUpdate']]) });
    render(card(new Set(['a', 'b']), none), { wrapper });
    expect(screen.getByLabelText('Working sessions')).toBeTruthy();
    expect(screen.getByText('Polynux split')).toBeTruthy();
    expect(screen.getByText('SERP migration')).toBeTruthy();
    expect(screen.getByText('TaskUpdate')).toBeTruthy();
    expect(screen.getAllByText('nuxtpolymarket')).toHaveLength(2);
  });

  it('shows a finished session as Done until it is opened', () => {
    const onSelect = vi.fn();
    const { rerender } = render(card(new Set(['a', 'b']), none, onSelect), { wrapper });
    rerender(card(new Set(['b']), new Set(['a']), onSelect));
    expect(screen.getByText('Done')).toBeTruthy();

    fireEvent.click(screen.getByText('Polynux split'));
    expect(onSelect).toHaveBeenCalledWith('a');

    // Opening it clears the completion: one session left, so the card goes.
    rerender(card(new Set(['b']), none, onSelect));
    expect(screen.queryByLabelText('Working sessions')).toBeNull();
  });
});
