// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Chat } from '@/types/chat.types';
import { useProfileStore } from '@/store/profileStore';
import { useThreadProfile } from './useThreadProfile';

const h = vi.hoisted(() => ({
  navigate: vi.fn(),
  updateChat: vi.fn(),
}));

vi.mock('react-router-dom', async (importOriginal) => ({
  ...(await importOriginal<typeof import('react-router-dom')>()),
  useNavigate: () => h.navigate,
}));
vi.mock('@/services/chatService', () => ({
  chatService: { updateChat: h.updateChat },
}));
vi.mock('react-hot-toast', () => ({ default: { error: vi.fn(), success: vi.fn() } }));

const chat = (over: Partial<Chat> = {}): Chat => ({
  id: 'c1',
  user_id: 'u1',
  title: 'Thread',
  workspace_id: 'ws1',
  sandbox_id: 'sb1',
  created_at: '',
  updated_at: '',
  pinned_at: null,
  worktree_cwd: null,
  session_agent_kind: null,
  unread: false,
  last_model_id: null,
  last_thinking_mode: null,
  last_persona_name: null,
  profile_id: 'builtin',
  provider: 'claude',
  ...over,
});

function setup(options: { chat: Chat; hasMessages: boolean; draft?: string }) {
  const setDraft = vi.fn();
  const client = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const { result } = renderHook(
    () =>
      useThreadProfile({
        chat: options.chat,
        hasMessages: options.hasMessages,
        draft: options.draft ?? '',
        setDraft,
      }),
    { wrapper },
  );
  return { result, setDraft };
}

beforeEach(() => {
  useProfileStore.setState({ lastProfileId: null });
  h.updateChat.mockImplementation(async (chatId: string, data: { profile_id: string }) =>
    chat({ id: chatId, profile_id: data.profile_id, provider: 'codex' }),
  );
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('useThreadProfile', () => {
  it('exposes the thread profile, its provider and the lock', () => {
    const { result } = setup({
      chat: chat({ profile_id: 'p2', provider: 'codex' }),
      hasMessages: true,
    });
    expect(result.current).toMatchObject({
      profileId: 'p2',
      agentKind: 'codex',
      profileLocked: true,
    });
  });

  it('switches an empty thread in place', async () => {
    const { result } = setup({ chat: chat(), hasMessages: false });
    act(() => result.current.onProfileChange('p2'));
    await waitFor(() => expect(h.updateChat).toHaveBeenCalledWith('c1', { profile_id: 'p2' }));
    expect(h.navigate).not.toHaveBeenCalled();
    expect(useProfileStore.getState().lastProfileId).toBe('p2');
  });

  it('opens a new thread in the same project for a thread with messages', () => {
    const { result, setDraft } = setup({
      chat: chat(),
      hasMessages: true,
      draft: 'half-written prompt',
    });
    act(() => result.current.onProfileChange('p2'));
    expect(h.updateChat).not.toHaveBeenCalled();
    expect(h.navigate).toHaveBeenCalledWith('/', {
      state: { workspaceId: 'ws1', initialMessage: 'half-written prompt' },
    });
    // The draft moves to the new thread, and the new thread starts on the pick.
    expect(setDraft).toHaveBeenCalledWith('');
    expect(useProfileStore.getState().lastProfileId).toBe('p2');
  });

  it('carries no draft when there is none', () => {
    const { result, setDraft } = setup({ chat: chat(), hasMessages: true, draft: '   ' });
    act(() => result.current.onProfileChange('p2'));
    expect(h.navigate).toHaveBeenCalledWith('/', {
      state: { workspaceId: 'ws1', initialMessage: undefined },
    });
    expect(setDraft).not.toHaveBeenCalled();
  });

  it('ignores picking the current profile', () => {
    const { result } = setup({ chat: chat(), hasMessages: true });
    act(() => result.current.onProfileChange('builtin'));
    expect(h.navigate).not.toHaveBeenCalled();
    expect(h.updateChat).not.toHaveBeenCalled();
    expect(useProfileStore.getState().lastProfileId).toBeNull();
  });
});
