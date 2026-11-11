import { useMemo } from 'react';
import { useUIStore } from '@/store/uiStore';
import { useAuthStore } from '@/store/authStore';
import { useIsMobile } from '@/hooks/useIsMobile';
import { activeSplitSlot } from '@/utils/tileHelpers';
import { useChatContext } from '@/hooks/useChatContext';
import { useActiveChat } from '@/hooks/useActiveChat';
import { useGitBranchesQuery, useCheckoutBranchMutation } from '@/hooks/queries/useSandboxQueries';
import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import { useSidebarChatLists } from '@/hooks/queries/useSidebarChatLists';
import { fuzzySearch } from '@/utils/fuzzySearch';
import { THEMES } from '@/utils/theme';
import {
  SETTING_ROWS,
  type ChatRowItem,
  type MenuListItem,
  type MenuMode,
} from './commandRegistry';

export function useCommandMenuData(mode: MenuMode, query: string) {
  const isOpen = useUIStore((state) => state.commandMenuOpen);
  const theme = useUIStore((state) => state.theme);
  const isMobile = useIsMobile();
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const primaryCtx = useChatContext();

  // Branch switching targets the pane the user last interacted with — in
  // split view each split pane has its own repo/cwd.
  const activeAgentTile = useUIStore((s) =>
    s.commandMenuOpen ? s.activeAgentTile : 'agent:primary',
  );
  const splitChatIds = useUIStore((s) => s.splitChatIds);
  const activeSlot = activeSplitSlot(activeAgentTile, splitChatIds);
  // Resolve the active pane's chat through the canonical hook, gated on the menu
  // being open so the always-mounted menu doesn't re-render on pane/split churn
  // while closed. Only used for a split pane — the primary reuses the
  // chat context directly below.
  const activeChat = useActiveChat(isOpen);
  const splitChat = activeSlot ? activeChat : undefined;

  const sandboxId = activeSlot ? (splitChat?.sandbox_id ?? undefined) : primaryCtx.sandboxId;
  const worktreeCwd = activeSlot ? (splitChat?.worktree_cwd ?? undefined) : primaryCtx.worktreeCwd;

  // Fetch branches whenever the menu is open so we can both render the branches mode and
  // filter the switch-branch command out of the list for chats without a repo. Cache is
  // usually warm from BranchSelector so this rarely triggers a real fetch.
  const { data: branchesData } = useGitBranchesQuery(sandboxId, isOpen && !!sandboxId, worktreeCwd);
  const checkoutBranch = useCheckoutBranchMutation();

  // Chat rows reuse the sidebar's queries (cache-warm on every page the menu mounts on),
  // gated on open + auth so the always-mounted menu doesn't fetch while closed or logged out.
  const chatQueriesEnabled = isOpen && isAuthenticated;
  const workspaces = useWorkspacesList({ enabled: chatQueriesEnabled });
  const { pinnedChats, recentChats, workspaceBadgeById } = useSidebarChatLists(
    workspaces,
    chatQueriesEnabled,
  );
  const menuChats = useMemo(() => {
    if (!isOpen) return [];
    // Pinned and recents are disjoint; the menu is a single recency-ordered list.
    return [...pinnedChats, ...recentChats].sort(
      (a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at),
    );
  }, [isOpen, pinnedChats, recentChats]);

  const filteredChats = useMemo(
    () =>
      mode !== 'all' && mode !== 'chats'
        ? []
        : fuzzySearch(query, menuChats, { keys: ['title'], limit: mode === 'chats' ? 30 : 5 }),
    [query, menuChats, mode],
  );

  const trimmedQuery = query.trim();

  const chatRows = useMemo<ChatRowItem[]>(
    () =>
      filteredChats.map((chat) => ({
        id: chat.id,
        title: chat.title,
        workspaceName: workspaceBadgeById.get(chat.workspace_id)?.name,
      })),
    [filteredChats, workspaceBadgeById],
  );

  // Settings join "All" only once something is typed; recent chats lead otherwise.
  const filteredSettings = useMemo(
    () =>
      mode === 'settings' || (mode === 'all' && query.trim())
        ? fuzzySearch(query, SETTING_ROWS, { keys: ['label'], limit: 20 })
        : [],
    [query, mode],
  );

  const listItems = useMemo<MenuListItem[]>(
    () => [
      ...chatRows.map((chat) => ({ kind: 'chat' as const, chat })),
      ...filteredSettings.map((setting) => ({ kind: 'setting' as const, setting })),
    ],
    [chatRows, filteredSettings],
  );

  const orderedBranches = useMemo(() => {
    if (!branchesData) return [];
    const current = branchesData.current_branch;
    const others = branchesData.branches.filter((b) => b !== current);
    return current ? [current, ...others] : others;
  }, [branchesData]);

  const filteredBranches = useMemo(
    () => (mode !== 'branches' ? [] : fuzzySearch(query, orderedBranches, { limit: 30 })),
    [mode, query, orderedBranches],
  );

  const filteredThemes = useMemo(
    () => (mode !== 'themes' ? [] : fuzzySearch(query, THEMES, { keys: ['label'], limit: 30 })),
    [mode, query],
  );

  const listLength =
    mode === 'branches'
      ? filteredBranches.length
      : mode === 'themes'
        ? filteredThemes.length
        : listItems.length;

  return {
    isOpen,
    isMobile,
    theme,
    sandboxId,
    worktreeCwd,
    branchesData,
    checkoutBranch,
    listItems,
    listLength,
    filteredBranches,
    filteredThemes,
    trimmedQuery,
  };
}
