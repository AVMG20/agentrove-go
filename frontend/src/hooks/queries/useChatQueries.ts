import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import type {
  UseMutationOptions,
  UseQueryOptions,
  InfiniteData,
  Query,
  QueryClient,
  QueryFilters,
  QueryKey,
} from '@tanstack/react-query';
import { chatService } from '@/services/chatService';
import { useMessageQueueStore } from '@/store/messageQueueStore';
import { useComposerDraftStore } from '@/store/composerDraftStore';
import { useUIStore, type ComposerSelection } from '@/store/uiStore';
import type {
  Chat,
  ChatSearchResponse,
  ContextUsage,
  CreateChatRequest,
  UpdateChatRequest,
} from '@/types/chat.types';
import type { PaginatedChats } from '@/types/api.types';
import { logger } from '@/utils/logger';
import { createMutation } from './createMutation';
import { snapshotQueries, type CacheSnapshot } from './optimistic';
import { queryKeys } from './queryKeys';

const CHATS_PER_PAGE = 25;
const RECENT_CHATS_PER_PAGE = 100;
const GLOBAL_WORKSPACE_SENTINEL = 'all';

// Global infinite chats: key[3]=GLOBAL_WORKSPACE_SENTINEL, key[4]=null (unpinned).
function isGlobalChatsQuery(query: Query): boolean {
  const key = query.queryKey;
  return key.length >= 5 && key[3] === GLOBAL_WORKSPACE_SENTINEL && key[4] === null;
}

export const useInfiniteChatsQuery = (options?: {
  perPage?: number;
  workspaceId?: string;
  pinned?: boolean;
  enabled?: boolean;
}) => {
  const perPage = options?.perPage ?? CHATS_PER_PAGE;
  const workspaceId = options?.workspaceId;
  const pinned = options?.pinned;

  return useInfiniteQuery({
    queryKey: [
      queryKeys.chats,
      'infinite',
      perPage,
      workspaceId ?? GLOBAL_WORKSPACE_SENTINEL,
      pinned ?? null,
    ] as const,
    queryFn: async ({ pageParam }) => {
      const page = pageParam as number;
      return chatService.listChats({
        page,
        per_page: perPage,
        workspace_id: workspaceId,
        pinned,
      });
    },
    getNextPageParam: (lastPage) => {
      const nextPage = lastPage.page + 1;
      return nextPage <= lastPage.pages ? nextPage : undefined;
    },
    initialPageParam: 1,
    enabled: options?.enabled ?? true,
    gcTime: 1000 * 60 * 1,
    // Snapshot hydrates first paint; always refetch so out-of-band creates surface.
    refetchOnMount: 'always',
  });
};

// Archived chats, most recently archived first (Settings → Archived). Under
// [chats, 'infinite'] so list invalidations (a new turn restores) reach it.
export const useArchivedChatsQuery = () => {
  return useInfiniteQuery({
    queryKey: [queryKeys.chats, 'infinite', 'archived'] as const,
    queryFn: ({ pageParam }) =>
      chatService.listChats({ page: pageParam, per_page: CHATS_PER_PAGE, archived: true }),
    getNextPageParam: (lastPage) => {
      const nextPage = lastPage.page + 1;
      return nextPage <= lastPage.pages ? nextPage : undefined;
    },
    initialPageParam: 1,
    refetchOnMount: 'always',
  });
};

// Backs the composer's `@` chat mentions: one fetch per staleTime window, filtered
// client-side like files.
export const useRecentChatsQuery = (enabled: boolean) => {
  return useQuery({
    queryKey: queryKeys.chatsRecent,
    queryFn: () =>
      chatService.listChats({
        page: 1,
        per_page: RECENT_CHATS_PER_PAGE,
      }),
    enabled,
    staleTime: 1000 * 60 * 5,
  });
};

export const useSearchChatsQuery = (
  query: string,
  options?: Partial<UseQueryOptions<ChatSearchResponse>>,
) => {
  const trimmed = query.trim();
  return useQuery({
    queryKey: queryKeys.chatsSearch(trimmed),
    queryFn: () => chatService.searchChats(trimmed),
    enabled: trimmed.length >= 2,
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    ...options,
  });
};

export const useInfiniteMessagesQuery = (chatId: string | undefined, limit: number = 20) => {
  return useInfiniteQuery({
    queryKey: queryKeys.messages(chatId),
    queryFn: async ({ pageParam }) => {
      return chatService.getMessages(chatId!, {
        cursor: pageParam as string | undefined,
        limit,
      });
    },
    getNextPageParam: (lastPage) => lastPage.next_cursor ?? undefined,
    initialPageParam: undefined as string | undefined,
    enabled: !!chatId,
    // Pages are small once tool results are omitted, so switching back to a
    // recent chat can skip the download.
    gcTime: 1000 * 60 * 10,
  });
};

// Completed messages are immutable, so the full render never goes stale.
export const useMessageRenderQuery = (messageId: string, enabled: boolean) => {
  return useQuery({
    queryKey: queryKeys.messageRender(messageId),
    queryFn: () => chatService.getMessageRender(messageId),
    enabled,
    staleTime: Infinity,
  });
};

export const useChatQuery = (
  chatId: string | undefined,
  options?: Partial<UseQueryOptions<Chat>>,
) => {
  return useQuery({
    queryKey: queryKeys.chat(chatId),
    queryFn: () => chatService.getChat(chatId!),
    enabled: !!chatId,
    ...options,
  });
};

export const useContextUsageQuery = (
  chatId: string | undefined,
  options?: Partial<UseQueryOptions<ContextUsage>>,
) => {
  return useQuery({
    queryKey: queryKeys.contextUsage(chatId),
    queryFn: () => chatService.getContextUsage(chatId!),
    enabled: !!chatId,
    staleTime: 0,
    ...options,
  });
};

// Shared by create-chat mutation + chat_created SSE. Dedup by id — both paths fire
// for local creates; second-pass invalidations are intentional (don't suppress).
export async function applyCreatedChat(queryClient: QueryClient, newChat: Chat): Promise<void> {
  queryClient.setQueryData(queryKeys.chat(newChat.id), newChat);
  queryClient.invalidateQueries({ queryKey: queryKeys.chatsRecent });

  queryClient.setQueriesData<InfiniteData<PaginatedChats>>(
    { queryKey: [queryKeys.chats, 'infinite'], predicate: isGlobalChatsQuery },
    (oldData) => {
      if (!oldData) return oldData;
      if (oldData.pages.some((page) => page.items.some((chat) => chat.id === newChat.id))) {
        return oldData;
      }
      return {
        ...oldData,
        pages: oldData.pages.map((page, index) =>
          index === 0 ? { ...page, items: [newChat, ...page.items], total: page.total + 1 } : page,
        ),
      };
    },
  );

  queryClient.invalidateQueries({
    queryKey: [queryKeys.chats, 'infinite'],
    predicate: (query) => !isGlobalChatsQuery(query),
  });

  queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
}

// Patch single-chat query + every infinite chats list + recent-chats mention list together.
export function patchChatInCache(
  queryClient: QueryClient,
  chatId: string,
  patch: (chat: Chat) => Chat,
) {
  queryClient.setQueryData<Chat>(queryKeys.chat(chatId), (prev) => (prev ? patch(prev) : prev));
  queryClient.setQueriesData<InfiniteData<PaginatedChats>>(
    { queryKey: [queryKeys.chats, 'infinite'] },
    (oldData) => {
      if (!oldData) return oldData;
      return {
        ...oldData,
        pages: oldData.pages.map((page) => ({
          ...page,
          items: page.items.map((chat) => (chat.id === chatId ? patch(chat) : chat)),
        })),
      };
    },
  );
  queryClient.setQueryData<PaginatedChats>(queryKeys.chatsRecent, (oldData) => {
    if (!oldData) return oldData;
    return {
      ...oldData,
      items: oldData.items.map((chat) => (chat.id === chatId ? patch(chat) : chat)),
    };
  });
}

// Optimistic unread clear + server stamp; fire-and-forget (failed stamp resurfaces on refetch).
export async function markChatViewed(queryClient: QueryClient, chatId: string): Promise<void> {
  patchChatInCache(queryClient, chatId, (chat) =>
    chat.unread ? { ...chat, unread: false } : chat,
  );

  try {
    await chatService.markChatViewed(chatId);
  } catch (error) {
    logger.error('Failed to mark chat viewed', 'markChatViewed', error);
  }
}

export const useCreateChatMutation = createMutation<Chat, Error, CreateChatRequest>(
  (data) => chatService.createChat(data),
  (queryClient, newChat) => applyCreatedChat(queryClient, newChat),
);

// Every list a chat can sit in, for snapshots before an optimistic change.
function chatCacheFilters(chatId: string): QueryFilters[] {
  return [
    { queryKey: [queryKeys.chats, 'infinite'] },
    { queryKey: queryKeys.chatsRecent, exact: true },
    { queryKey: queryKeys.chat(chatId), exact: true },
  ];
}

// The freshest copy of a chat in the cache, from the detail query or any list.
function findCachedChat(queryClient: QueryClient, chatId: string): Chat | undefined {
  const detail = queryClient.getQueryData<Chat>(queryKeys.chat(chatId));
  if (detail) return detail;
  for (const [, data] of queryClient.getQueriesData<InfiniteData<PaginatedChats>>({
    queryKey: [queryKeys.chats, 'infinite'],
  })) {
    const found = data?.pages.flatMap((page) => page.items).find((chat) => chat.id === chatId);
    if (found) return found;
  }
  return queryClient
    .getQueryData<PaginatedChats>(queryKeys.chatsRecent)
    ?.items.find((chat) => chat.id === chatId);
}

const isArchivedList = (key: QueryKey) => key[2] === 'archived';

// Whether a thread list would hold the chat: key[3] is its workspace (or all),
// key[4] its pinned filter (null for either).
function threadListHolds(key: QueryKey, chat: Chat): boolean {
  if (isArchivedList(key) || chat.archived_at) return false;
  const workspace = key[3];
  const pinned = key[4];
  return (
    (workspace === GLOBAL_WORKSPACE_SENTINEL || workspace === chat.workspace_id) &&
    (pinned === null || pinned === !!chat.pinned_at)
  );
}

const time = (value: string | null | undefined) => (value ? Date.parse(value) : 0);

// The server's list order: pinned first (newest pin first), then by activity;
// archived chats by when they were archived.
function listsBefore(key: QueryKey, a: Chat, b: Chat): boolean {
  if (isArchivedList(key)) return time(a.archived_at) > time(b.archived_at);
  if (!!a.pinned_at !== !!b.pinned_at) return !!a.pinned_at;
  if (a.pinned_at && b.pinned_at && a.pinned_at !== b.pinned_at) {
    return time(a.pinned_at) > time(b.pinned_at);
  }
  return time(a.updated_at) > time(b.updated_at);
}

function removeFromPages(
  data: InfiniteData<PaginatedChats> | undefined,
  chatId: string,
): InfiniteData<PaginatedChats> | undefined {
  if (!data?.pages.some((page) => page.items.some((chat) => chat.id === chatId))) return data;
  return {
    ...data,
    pages: data.pages.map((page) => ({
      ...page,
      items: page.items.filter((chat) => chat.id !== chatId),
      total: Math.max(0, page.total - 1),
    })),
  };
}

// Puts the chat at its sorted place among the loaded pages. Past the last
// loaded chat only once every page is loaded; otherwise Load more brings it.
function insertIntoPages(
  data: InfiniteData<PaginatedChats> | undefined,
  chat: Chat,
  before: (a: Chat, b: Chat) => boolean,
): InfiniteData<PaginatedChats> | undefined {
  if (!data || data.pages.length === 0) return data;
  let pageIndex = data.pages.findIndex((page) => page.items.some((item) => before(chat, item)));
  let itemIndex: number;
  if (pageIndex !== -1) {
    itemIndex = data.pages[pageIndex].items.findIndex((item) => before(chat, item));
  } else {
    const last = data.pages[data.pages.length - 1];
    if (last.page < last.pages) return data;
    pageIndex = data.pages.length - 1;
    itemIndex = last.items.length;
  }
  return {
    ...data,
    pages: data.pages.map((page, index) => ({
      ...page,
      items:
        index === pageIndex
          ? [...page.items.slice(0, itemIndex), chat, ...page.items.slice(itemIndex)]
          : page.items,
      total: page.total + 1,
    })),
  };
}

// Moves the chat to where its current fields put it in every cached chat list
// (thread lists and the archived one), and patches the detail and mention list.
function placeChatInCache(queryClient: QueryClient, chat: Chat) {
  queryClient.setQueryData<Chat>(queryKeys.chat(chat.id), (prev) => (prev ? chat : prev));
  for (const query of queryClient.getQueryCache().findAll({
    queryKey: [queryKeys.chats, 'infinite'],
  })) {
    const key = query.queryKey;
    const holds = isArchivedList(key) ? !!chat.archived_at : threadListHolds(key, chat);
    queryClient.setQueryData<InfiniteData<PaginatedChats>>(key, (data) => {
      const without = removeFromPages(data, chat.id);
      return holds ? insertIntoPages(without, chat, (a, b) => listsBefore(key, a, b)) : without;
    });
  }
  queryClient.setQueryData<PaginatedChats>(queryKeys.chatsRecent, (old) => {
    if (!old) return old;
    // Archived chats leave the mentions; a restored one returns on refetch.
    const items = chat.archived_at
      ? old.items.filter((item) => item.id !== chat.id)
      : old.items.map((item) => (item.id === chat.id ? chat : item));
    return { ...old, items };
  });
}

// Drops the chat from every cached list.
function removeChatFromLists(queryClient: QueryClient, chatId: string) {
  queryClient.setQueriesData<InfiniteData<PaginatedChats>>(
    { queryKey: [queryKeys.chats, 'infinite'] },
    (data) => removeFromPages(data, chatId),
  );
  queryClient.setQueryData<PaginatedChats>(queryKeys.chatsRecent, (old) =>
    old ? { ...old, items: old.items.filter((chat) => chat.id !== chatId) } : old,
  );
}

// Snapshots the chat's caches, then moves the chat as `change` would leave it.
// The server stamps updated_at on every edit, so the chat moves up like it will.
async function applyChatChange(
  queryClient: QueryClient,
  chatId: string,
  change: (chat: Chat, now: string) => Chat,
): Promise<CacheSnapshot> {
  const snapshot = await snapshotQueries(queryClient, chatCacheFilters(chatId));
  const chat = findCachedChat(queryClient, chatId);
  if (chat) {
    const now = new Date().toISOString();
    placeChatInCache(queryClient, { ...change(chat, now), updated_at: now });
  }
  return snapshot;
}

// Shared by the optimistic chat edits: each settles (refetches) only once none
// is in flight, so one's refetch can't undo another's change mid-request.
const chatMutationKey = ['chat-mutation'] as const;

function settleChatLists(queryClient: QueryClient) {
  void queryClient.invalidateQueries({ queryKey: [queryKeys.chats, 'infinite'] });
  void queryClient.invalidateQueries({ queryKey: queryKeys.chatsRecent });
  void queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
  void queryClient.invalidateQueries({ queryKey: queryKeys.chatsSearchAll });
}

// A title edit is optimistic (the chat renames and moves up at once); a
// profile switch waits for the server, which checks the thread is still empty.
export const useUpdateChatMutation = createMutation<
  Chat,
  Error,
  { chatId: string; updateData: UpdateChatRequest }
>(
  ({ chatId, updateData }) => chatService.updateChat(chatId, updateData),
  (queryClient, updatedChat, _variables, { isLast }) => {
    if (isLast) placeChatInCache(queryClient, updatedChat);
  },
  {
    mutationKey: chatMutationKey,
    scope: 'chat-update',
    apply: async (queryClient, { chatId, updateData }) => {
      const { title } = updateData;
      if (title === undefined || Object.keys(updateData).length > 1) return undefined;
      return applyChatChange(queryClient, chatId, (chat) => ({ ...chat, title }));
    },
    settle: settleChatLists,
  },
);

// Optimistic: the chat moves between the Pinned and Recents lists at once.
export const usePinChatMutation = createMutation<Chat, Error, { chatId: string; pinned: boolean }>(
  ({ chatId, pinned }) => (pinned ? chatService.pinChat(chatId) : chatService.unpinChat(chatId)),
  (queryClient, updatedChat, _variables, { isLast }) => {
    if (isLast) queryClient.setQueryData(queryKeys.chat(updatedChat.id), updatedChat);
  },
  {
    mutationKey: chatMutationKey,
    scope: 'chat-pin',
    apply: (queryClient, { chatId, pinned }) =>
      applyChatChange(queryClient, chatId, (chat, now) => ({
        ...chat,
        pinned_at: pinned ? now : null,
      })),
    settle: settleChatLists,
  },
);

// Optimistic both ways: archiving leaves the thread lists for the archived one
// at once, restoring (and Undo) goes back. Requests run in call order, so an
// Undo right after archiving lands after it.
export const useArchiveChatMutation = createMutation<
  Chat,
  Error,
  { chatId: string; archived: boolean }
>(
  ({ chatId, archived }) => chatService.setChatArchived(chatId, archived),
  (queryClient, updatedChat, _variables, { isLast }) => {
    if (isLast) queryClient.setQueryData(queryKeys.chat(updatedChat.id), updatedChat);
  },
  {
    mutationKey: chatMutationKey,
    scope: 'chat-archive',
    apply: (queryClient, { chatId, archived }) =>
      applyChatChange(queryClient, chatId, (chat, now) => ({
        ...chat,
        archived_at: archived ? now : null,
      })),
    settle: settleChatLists,
  },
);

// Optimistic: the chat leaves every list at once; its caches and local state
// are dropped once the server confirms.
export const useDeleteChatMutation = createMutation<void, Error, string>(
  (chatId) => chatService.deleteChat(chatId),
  (queryClient, _data, chatId) => {
    queryClient.removeQueries({ queryKey: queryKeys.chat(chatId) });
    queryClient.removeQueries({ queryKey: queryKeys.messages(chatId) });
    queryClient.removeQueries({ queryKey: queryKeys.contextUsage(chatId) });
    useMessageQueueStore.getState().cleanupChat(chatId);
    useUIStore.getState().cleanupChat(chatId);
    useComposerDraftStore.getState().clearDraft(chatId);
  },
  {
    mutationKey: chatMutationKey,
    apply: async (queryClient, chatId) => {
      const snapshot = await snapshotQueries(queryClient, chatCacheFilters(chatId));
      removeChatFromLists(queryClient, chatId);
      return snapshot;
    },
    settle: settleChatLists,
  },
);

export const useDeleteAllChatsMutation = () => {
  const queryClient = useQueryClient();
  return useMutation<void, Error, void>({
    mutationFn: async () => {
      await chatService.deleteAllChats();
      // Prefix ['chats'] also clears chatsSearch.
      queryClient.removeQueries({ queryKey: [queryKeys.chats] });
      queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
      useUIStore.getState().cleanupAllChats();
    },
  });
};

interface EnhancePromptParams {
  prompt: string;
  modelId: string;
}

export const useGenerateChatTitleMutation = () => {
  return useMutation({
    mutationFn: (chatId: string) => chatService.generateChatTitle(chatId),
  });
};

export const useEnhancePromptMutation = (
  options?: UseMutationOptions<string, Error, EnhancePromptParams>,
) => {
  return useMutation({
    mutationFn: ({ prompt, modelId }: EnhancePromptParams) =>
      chatService.enhancePrompt(prompt, modelId),
    ...options,
  });
};

interface AskAboutCodeParams {
  chatId: string;
  selection: ComposerSelection;
  question: string;
  modelId: string;
}

export const useAskAboutCodeMutation = () => {
  return useMutation({
    mutationFn: ({ chatId, selection, question, modelId }: AskAboutCodeParams) =>
      chatService.askAboutCode(chatId, selection, question, modelId),
  });
};
