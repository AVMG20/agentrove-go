import { useCallback, useMemo } from 'react';
import { useInfiniteChatsQuery } from '@/hooks/queries/useChatQueries';
import type { Workspace } from '@/types/workspace.types';

export interface WorkspaceBadge {
  name: string;
}

// Pinned and unpinned chats as the sidebar's Pinned/Recents lists.
export function useSidebarChatLists(workspaces: Workspace[], enabled: boolean = true) {
  const { data: pinnedChatsData } = useInfiniteChatsQuery({ pinned: true, enabled });
  const {
    data: chatsData,
    hasNextPage,
    fetchNextPage,
    isFetchingNextPage,
    isLoading,
  } = useInfiniteChatsQuery({ pinned: false, enabled });

  const pinnedChats = useMemo(() => {
    const pinned = pinnedChatsData?.pages.flatMap((page) => page.items ?? []) ?? [];
    return [...pinned].sort((a, b) => Date.parse(b.pinned_at!) - Date.parse(a.pinned_at!));
  }, [pinnedChatsData?.pages]);

  // One recency-ordered list spanning every workspace.
  const recentChats = useMemo(() => {
    const chats = chatsData?.pages.flatMap((page) => page.items ?? []) ?? [];
    const sorted = [...chats].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at));
    // While pages remain unloaded, anything older than the oldest loaded chat can't be
    // placed yet — an unloaded chat may be newer. Load more advances the window.
    if (!hasNextPage || chats.length === 0) return sorted;
    const floor = Date.parse(chats[chats.length - 1].updated_at);
    return sorted.filter((chat) => Date.parse(chat.updated_at) >= floor);
  }, [chatsData?.pages, hasNextPage]);

  const workspaceBadgeById = useMemo(() => {
    const map = new Map<string, WorkspaceBadge>();
    for (const workspace of workspaces) {
      map.set(workspace.id, { name: workspace.name });
    }
    return map;
  }, [workspaces]);

  const loadMore = useCallback(() => {
    if (hasNextPage) void fetchNextPage();
  }, [hasNextPage, fetchNextPage]);

  return {
    pinnedChats,
    recentChats,
    workspaceBadgeById,
    isLoadingChats: isLoading,
    hasMore: !!hasNextPage,
    isFetchingMore: isFetchingNextPage,
    loadMore,
  };
}
