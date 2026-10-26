import { useEffect, useMemo, useCallback, useRef } from 'react';
import { useMatch, useNavigate } from 'react-router-dom';
import type { Chat } from '@/types/chat.types';
import { useSidebarChatLists } from '@/hooks/queries/useSidebarChatLists';
import { useUIStore } from '@/store/uiStore';
import { useStreamStore } from '@/store/streamStore';
import { useBlockedChatIds } from '@/hooks/useBlockedChatIds';
import { useWorkingChatIds } from '@/hooks/useWorkingChatIds';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useChatQuery } from '@/hooks/queries/useChatQueries';
import { useWorkspacesList, useWorkspacesQuery } from '@/hooks/queries/useWorkspaceQueries';
import { SidebarActions } from './SidebarActions';
import { SidebarChatList } from './SidebarChatList';
import { SidebarOverlays } from './SidebarOverlays';
import { type ChatRowProps } from './SidebarChatRow';
import { buildRecentChatSections } from './sidebarGrouping';
import { useSidebarChatActions } from './useSidebarChatActions';
import { useSidebarWorkspaceActions } from './useSidebarWorkspaceActions';
import { countActiveSidebarFilters, dropMissingWorkspaceFilter } from '@/store/sidebarFilters';
import styles from './ThreadsPanel.module.scss';

// The sidebar's thread browser: primary actions and the chat list.
// Selection comes from the route; the open chat record is read from
// the same query cache the page uses, so this adds no requests.
export function ThreadsPanel({ hidden = false }: { hidden?: boolean }) {
  const navigate = useNavigate();
  const isMobile = useIsMobile();
  const selectedChatId = useMatch('/chat/:chatId')?.params.chatId ?? null;
  const workspaces = useWorkspacesList();
  const { data: selectedChat } = useChatQuery(selectedChatId ?? undefined);
  const selectedChatWorkspaceId = selectedChat?.workspace_id;
  // Chats whose stream finished successfully since last viewed — drives the "Done" badge
  const completedChatIds = useStreamStore((state) => state.completedChatIds);
  const splitChatIds = useUIStore((state) => state.splitChatIds);
  const streamingChatIdSet = useWorkingChatIds();
  // Chats with a pending plan/permission request or agent question form — the
  // agent is blocked on the user.
  const blockedChatIdSet = useBlockedChatIds();
  // Filters apply client-side over loaded pages. Lives in the persisted uiStore
  // so reloads keep the narrowed view.
  const filters = useUIStore((state) => state.sidebarFilters);
  const { isSuccess: workspacesLoaded } = useWorkspacesQuery();
  useEffect(() => {
    if (!workspacesLoaded) return;
    const next = dropMissingWorkspaceFilter(
      filters,
      workspaces.map((w) => w.id),
    );
    if (next !== filters) useUIStore.getState().setSidebarFilters(next);
  }, [workspacesLoaded, workspaces, filters]);

  const scrollContainerRef = useRef<HTMLDivElement>(null);

  const {
    pinnedChats,
    recentChats,
    workspaceBadgeById,
    isLoadingChats,
    hasMore,
    isFetchingMore,
    loadMore,
  } = useSidebarChatLists(workspaces);

  const chatActions = useSidebarChatActions({
    selectedChatId,
    isMobile,
    navigate,
    scrollContainerRef,
  });
  const workspaceActions = useSidebarWorkspaceActions({
    selectedChatId,
    selectedChatWorkspaceId,
    isMobile,
    navigate,
    scrollContainerRef,
  });

  const hasAnyContent = pinnedChats.length > 0 || recentChats.length > 0;

  const hasActiveFilters = countActiveSidebarFilters(filters) > 0;
  // Status filters OR together (any selected badge state matches); workspace ANDs with them.
  const chatMatchesFilters = useCallback(
    (chat: Chat) => {
      if (filters.workspaceId && chat.workspace_id !== filters.workspaceId) return false;
      if (filters.statuses.length === 0) return true;
      return (
        (filters.statuses.includes('unread') && chat.unread) ||
        (filters.statuses.includes('running') && streamingChatIdSet.has(chat.id)) ||
        (filters.statuses.includes('done') && completedChatIds.has(chat.id)) ||
        (filters.statuses.includes('needs-you') && blockedChatIdSet.has(chat.id))
      );
    },
    [filters, streamingChatIdSet, completedChatIds, blockedChatIdSet],
  );
  const visiblePinnedChats = useMemo(
    () => (hasActiveFilters ? pinnedChats.filter(chatMatchesFilters) : pinnedChats),
    [hasActiveFilters, pinnedChats, chatMatchesFilters],
  );
  const visibleRecentChats = useMemo(
    () => (hasActiveFilters ? recentChats.filter(chatMatchesFilters) : recentChats),
    [hasActiveFilters, recentChats, chatMatchesFilters],
  );
  const hasVisibleContent = visiblePinnedChats.length > 0 || visibleRecentChats.length > 0;

  const groupBy = filters.groupBy;
  const recentChatSections = useMemo(
    () =>
      buildRecentChatSections({
        groupBy,
        visibleRecentChats,
        workspaceBadgeById,
        blockedChatIdSet,
        streamingChatIdSet,
        completedChatIds,
      }),
    [
      groupBy,
      visibleRecentChats,
      workspaceBadgeById,
      blockedChatIdSet,
      streamingChatIdSet,
      completedChatIds,
    ],
  );

  // Opening a chat (or watching it finish while open) counts as seeing the
  // completion — clear its Done badge. Covers every open path since both ids
  // are route/layout-derived, not sidebar-click-derived.
  useEffect(() => {
    const store = useStreamStore.getState();
    if (selectedChatId && completedChatIds.has(selectedChatId)) {
      store.clearCompleted(selectedChatId);
    }
    for (const splitChatId of splitChatIds) {
      if (completedChatIds.has(splitChatId)) store.clearCompleted(splitChatId);
    }
  }, [selectedChatId, splitChatIds, completedChatIds]);

  const handleNewChat = useCallback(() => {
    navigate('/');
    if (isMobile) {
      useUIStore.getState().setSidebarOpen(false);
    }
  }, [navigate, isMobile]);

  const handleOpenSearch = useCallback(() => {
    useUIStore.getState().setCommandMenuOpen(true);
    // Close the drawer on mobile so the destination isn't hidden behind it after picking a result
    if (isMobile) {
      useUIStore.getState().setSidebarOpen(false);
    }
  }, [isMobile]);

  const rowProps: ChatRowProps = {
    selectedChatId,
    splitChatIds,
    dropdownChatId: chatActions.dropdown?.chat.id ?? null,
    streamingChatIdSet,
    blockedChatIdSet,
    completedChatIdSet: completedChatIds,
    workspaceBadgeById,
    onChatSelect: chatActions.handleChatSelect,
    onOpenInSplit: chatActions.showSplitAffordance ? chatActions.handleOpenInSplit : undefined,
    canOpenChatInSplit: chatActions.canOpenChatInSplit,
    onDropdownClick: chatActions.handleDropdownClick,
    onOpenWorkspaceMenu: workspaceActions.handleWorkspaceContextMenu,
  };

  return (
    <div className={styles.panel} hidden={hidden}>
      <SidebarActions onNewChat={handleNewChat} onOpenSearch={handleOpenSearch} />

      <SidebarChatList
        scrollContainerRef={scrollContainerRef}
        isLoadingChats={isLoadingChats}
        hasAnyContent={hasAnyContent}
        visiblePinnedChats={visiblePinnedChats}
        visibleRecentChats={visibleRecentChats}
        hasVisibleContent={hasVisibleContent}
        recentChatSections={recentChatSections}
        rowProps={rowProps}
        filters={filters}
        onChangeFilters={(next) => useUIStore.getState().setSidebarFilters(next)}
        workspaceBadgeById={workspaceBadgeById}
        hasMore={hasMore}
        isFetchingMore={isFetchingMore}
        onLoadMore={loadMore}
        onNewWorkspaceThread={workspaceActions.handleNewWorkspaceThread}
      />

      <SidebarOverlays chat={chatActions} workspace={workspaceActions} workspaces={workspaces} />
    </div>
  );
}
