import type { Chat } from '@/types/chat.types';
import type { WorkspaceBadge } from '@/hooks/queries/useSidebarChatLists';
import { SidebarChatItem } from './SidebarChatItem';

// Bundle row handlers/state so pinned and recent sections forward one object.
export interface ChatRowProps {
  selectedChatId: string | null;
  splitChatIds: string[];
  dropdownChatId: string | null;
  streamingChatIdSet: Set<string>;
  blockedChatIdSet: Set<string>;
  completedChatIdSet: Set<string>;
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  onChatSelect: (chatId: string) => void;
  onOpenInSplit?: (chatId: string) => void;
  canOpenChatInSplit: (chatId: string) => boolean;
  onDropdownClick: (e: React.MouseEvent<HTMLButtonElement>, chat: Chat) => void;
  // Project group headers' options menu (grouped view).
  onOpenWorkspaceMenu: (e: React.MouseEvent<HTMLButtonElement>, workspaceId: string) => void;
}

interface SidebarChatRowProps {
  chat: Chat;
  rowProps: ChatRowProps;
  // Inside a workspace folder the subtitle would repeat the folder name
  hideWorkspace?: boolean;
}

export function SidebarChatRow({ chat, rowProps, hideWorkspace = false }: SidebarChatRowProps) {
  const {
    selectedChatId,
    splitChatIds,
    dropdownChatId,
    streamingChatIdSet,
    blockedChatIdSet,
    completedChatIdSet,
    workspaceBadgeById,
    onChatSelect,
    onOpenInSplit,
    canOpenChatInSplit,
    onDropdownClick,
  } = rowProps;

  return (
    <SidebarChatItem
      chat={chat}
      isSelected={chat.id === selectedChatId}
      isActive={chat.id === selectedChatId || splitChatIds.includes(chat.id)}
      isDropdownOpen={dropdownChatId === chat.id}
      isChatStreaming={streamingChatIdSet.has(chat.id)}
      isChatBlocked={blockedChatIdSet.has(chat.id)}
      isChatCompleted={completedChatIdSet.has(chat.id)}
      workspaceBadge={hideWorkspace ? undefined : workspaceBadgeById.get(chat.workspace_id)}
      onSelect={onChatSelect}
      onOpenInSplit={onOpenInSplit}
      canOpenInSplit={canOpenChatInSplit(chat.id)}
      onDropdownClick={onDropdownClick}
    />
  );
}
