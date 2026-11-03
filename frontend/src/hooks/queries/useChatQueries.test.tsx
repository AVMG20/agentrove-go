// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider, type InfiniteData } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { PaginatedChats } from '@/types/api.types';
import type { Chat } from '@/types/chat.types';
import { queryKeys } from './queryKeys';
import { useArchiveChatMutation, usePinChatMutation } from './useChatQueries';

const { setChatArchived, pinChat } = vi.hoisted(() => ({
  setChatArchived: vi.fn<(chatId: string, archived: boolean) => Promise<Chat>>(),
  pinChat: vi.fn<(chatId: string) => Promise<Chat>>(),
}));
vi.mock('@/services/chatService', () => ({
  chatService: { setChatArchived, pinChat, unpinChat: vi.fn() },
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const HOUR = 60 * 60 * 1000;
const ago = (hours: number) => new Date(Date.now() - hours * HOUR).toISOString();

function chat(id: string, overrides: Partial<Chat> = {}): Chat {
  return {
    id,
    user_id: 'u1',
    title: id,
    workspace_id: 'ws1',
    sandbox_id: 's1',
    no_project: false,
    created_at: ago(100),
    updated_at: ago(1),
    pinned_at: null,
    archived_at: null,
    worktree_cwd: null,
    session_agent_kind: null,
    unread: false,
    last_model_id: null,
    last_thinking_mode: null,
    last_persona_name: null,
    profile_id: 'p1',
    provider: 'claude',
    ...overrides,
  };
}

function pages(items: Chat[]): InfiniteData<PaginatedChats> {
  return {
    pages: [{ items, page: 1, per_page: 25, total: items.length, pages: 1 }],
    pageParams: [1],
  };
}

const PINNED = [queryKeys.chats, 'infinite', 25, 'all', true];
const RECENTS = [queryKeys.chats, 'infinite', 25, 'all', false];
const WS1 = [queryKeys.chats, 'infinite', 25, 'ws1', false];
const WS2 = [queryKeys.chats, 'infinite', 25, 'ws2', false];
const ARCHIVED = [queryKeys.chats, 'infinite', 'archived'];

const ids = (client: QueryClient, key: unknown[]) =>
  client
    .getQueryData<InfiniteData<PaginatedChats>>(key)
    ?.pages.flatMap((page) => page.items.map((item) => item.id));

function setup() {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  const pinned = chat('pinned', { pinned_at: ago(5), updated_at: ago(5) });
  const c1 = chat('c1', { updated_at: ago(1) });
  const c2 = chat('c2', { updated_at: ago(2) });
  const c3 = chat('c3', { workspace_id: 'ws2', updated_at: ago(3) });
  const old = chat('old', { archived_at: ago(10), updated_at: ago(10) });
  client.setQueryData(PINNED, pages([pinned]));
  client.setQueryData(RECENTS, pages([c1, c2, c3]));
  client.setQueryData(WS1, pages([c1, c2]));
  client.setQueryData(WS2, pages([c3]));
  client.setQueryData(ARCHIVED, pages([old]));
  client.setQueryData(queryKeys.chat('c1'), c1);
  client.setQueryData(queryKeys.chatsRecent, pages([c1, c2, c3]).pages[0]);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  const archive = renderHook(() => useArchiveChatMutation(), { wrapper }).result;
  const pin = renderHook(() => usePinChatMutation(), { wrapper }).result;
  return { client, archive, pin, c1, old };
}

const flush = () => act(async () => {});

beforeEach(() => {
  setChatArchived.mockReset();
  pinChat.mockReset();
});
afterEach(cleanup);

describe('useArchiveChatMutation', () => {
  it('moves the chat to the archived list before the request resolves', async () => {
    const { client, archive, c1 } = setup();
    const request = deferred<Chat>();
    setChatArchived.mockReturnValue(request.promise);

    let done: Promise<Chat> | undefined;
    act(() => {
      done = archive.current.mutateAsync({ chatId: 'c1', archived: true });
    });
    await flush();

    expect(ids(client, RECENTS)).toEqual(['c2', 'c3']);
    expect(ids(client, WS1)).toEqual(['c2']);
    expect(ids(client, WS2)).toEqual(['c3']);
    expect(ids(client, PINNED)).toEqual(['pinned']);
    expect(ids(client, ARCHIVED)).toEqual(['c1', 'old']);
    expect(client.getQueryData<Chat>(queryKeys.chat('c1'))?.archived_at).toBeTruthy();
    expect(
      client.getQueryData<PaginatedChats>(queryKeys.chatsRecent)?.items.map((item) => item.id),
    ).toEqual(['c2', 'c3']);

    const server = { ...c1, archived_at: new Date().toISOString() };
    request.resolve(server);
    await act(async () => {
      await done;
    });
    expect(client.getQueryData(queryKeys.chat('c1'))).toEqual(server);
    // Settled: the lists refetch from the server.
    expect(client.getQueryState(RECENTS)?.isInvalidated).toBe(true);
    expect(client.getQueryState(ARCHIVED)?.isInvalidated).toBe(true);
  });

  it('puts everything back when the request fails', async () => {
    const { client, archive } = setup();
    const before = {
      recents: client.getQueryData(RECENTS),
      ws1: client.getQueryData(WS1),
      archived: client.getQueryData(ARCHIVED),
      detail: client.getQueryData(queryKeys.chat('c1')),
      mentions: client.getQueryData(queryKeys.chatsRecent),
    };
    const request = deferred<Chat>();
    setChatArchived.mockReturnValue(request.promise);

    let done: Promise<unknown> | undefined;
    act(() => {
      done = archive.current
        .mutateAsync({ chatId: 'c1', archived: true })
        .catch((error: unknown) => error);
    });
    await flush();
    expect(ids(client, RECENTS)).toEqual(['c2', 'c3']);

    request.reject(new Error('offline'));
    let error: unknown;
    await act(async () => {
      error = await done;
    });
    expect(error).toBeInstanceOf(Error);
    expect(client.getQueryData(RECENTS)).toEqual(before.recents);
    expect(client.getQueryData(WS1)).toEqual(before.ws1);
    expect(client.getQueryData(ARCHIVED)).toEqual(before.archived);
    expect(client.getQueryData(queryKeys.chat('c1'))).toEqual(before.detail);
    expect(client.getQueryData(queryKeys.chatsRecent)).toEqual(before.mentions);
  });

  it('restores into the lists that hold it, at the top since the server bumps it', async () => {
    const { client, archive, old } = setup();
    setChatArchived.mockReturnValue(new Promise(() => {}));

    act(() => {
      void archive.current.mutateAsync({ chatId: 'old', archived: false });
    });
    await flush();

    expect(ids(client, ARCHIVED)).toEqual([]);
    expect(ids(client, RECENTS)).toEqual(['old', 'c1', 'c2', 'c3']);
    expect(ids(client, WS1)).toEqual(['old', 'c1', 'c2']);
    expect(ids(client, WS2)).toEqual(['c3']);
    expect(ids(client, PINNED)).toEqual(['pinned']);
    const restored = client
      .getQueryData<InfiniteData<PaginatedChats>>(RECENTS)
      ?.pages[0].items.find((item) => item.id === old.id);
    expect(restored?.archived_at).toBeNull();
  });

  it('sends an Undo only after the archive it undoes, and keeps the Undo on screen', async () => {
    const { client, archive, c1 } = setup();
    const archiving = deferred<Chat>();
    const restoring = deferred<Chat>();
    setChatArchived.mockReturnValueOnce(archiving.promise).mockReturnValueOnce(restoring.promise);

    let first: Promise<Chat> | undefined;
    let second: Promise<Chat> | undefined;
    act(() => {
      first = archive.current.mutateAsync({ chatId: 'c1', archived: true });
    });
    await flush();
    act(() => {
      second = archive.current.mutateAsync({ chatId: 'c1', archived: false });
    });
    await flush();

    expect(ids(client, RECENTS)).toEqual(['c1', 'c2', 'c3']);
    expect(ids(client, ARCHIVED)).toEqual(['old']);
    expect(setChatArchived).toHaveBeenCalledTimes(1);

    // The archive's response is stale by now: it must not re-archive the chat.
    archiving.resolve({ ...c1, archived_at: new Date().toISOString() });
    await act(async () => {
      await first;
    });
    expect(client.getQueryData<Chat>(queryKeys.chat('c1'))?.archived_at).toBeNull();
    expect(client.getQueryState(RECENTS)?.isInvalidated).toBe(false);
    expect(setChatArchived).toHaveBeenLastCalledWith('c1', false);

    restoring.resolve({ ...c1, archived_at: null });
    await act(async () => {
      await second;
    });
    expect(client.getQueryState(RECENTS)?.isInvalidated).toBe(true);
  });
});

describe('usePinChatMutation', () => {
  it('moves the chat to the pinned list at once', async () => {
    const { client, pin } = setup();
    pinChat.mockReturnValue(new Promise(() => {}));

    act(() => {
      void pin.current.mutateAsync({ chatId: 'c2', pinned: true });
    });
    await flush();

    expect(ids(client, PINNED)).toEqual(['c2', 'pinned']);
    expect(ids(client, RECENTS)).toEqual(['c1', 'c3']);
    expect(ids(client, WS1)).toEqual(['c1']);
  });
});
