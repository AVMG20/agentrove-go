import type { Chat } from '@/types/chat.types';
import type { SidebarGroupBy, SidebarStatusFilter } from '@/store/sidebarFilters';
import type { WorkspaceBadge } from '@/hooks/queries/useSidebarChatLists';
import { formatDayLabel } from '@/utils/date';

export interface SidebarChatSection {
  key: string;
  label: string | null;
  chats: Chat[];
}

// Group order mirrors the row badge precedence in SidebarChatItem, so a chat
// lands in the group matching its visible badge; most urgent groups first.
const STATUS_GROUP_ORDER: { key: SidebarStatusFilter | 'other'; label: string }[] = [
  { key: 'needs-you', label: 'Needs you' },
  { key: 'running', label: 'Running' },
  { key: 'done', label: 'Done' },
  { key: 'unread', label: 'Unread' },
  { key: 'other', label: 'Other' },
];

interface GroupingContext {
  groupBy: SidebarGroupBy;
  visibleRecentChats: Chat[];
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  blockedChatIdSet: Set<string>;
  streamingChatIdSet: Set<string>;
  completedChatIds: Set<string>;
  // The user's saved order of workspace folders (user settings).
  folderOrder?: readonly string[] | null;
}

export type FolderDropPlacement = 'before' | 'after';

// Folders missing from the saved order (new projects, or none saved yet) come
// first in their incoming recency order, then the saved ones in saved order.
export function sortByFolderOrder<T>(
  items: T[],
  keyOf: (item: T) => string,
  order: readonly string[] | null | undefined,
): T[] {
  if (!order?.length) return items;
  const rank = new Map(order.map((key, index) => [key, index]));
  const unknown = items.filter((item) => !rank.has(keyOf(item)));
  const known = items
    .filter((item) => rank.has(keyOf(item)))
    .sort((a, b) => rank.get(keyOf(a))! - rank.get(keyOf(b))!);
  return [...unknown, ...known];
}

// The full order to save after dropping `key` before or after `targetKey`.
// `currentKeys` are the folders on screen in display order, then every other
// known project, so folders that aren't loaded or are filtered out keep their
// place. Null when nothing moves.
export function moveFolder(
  order: readonly string[] | null | undefined,
  currentKeys: readonly string[],
  key: string,
  targetKey: string,
  placement: FolderDropPlacement,
): string[] | null {
  if (key === targetKey) return null;
  const saved = order ?? [];
  const savedSet = new Set(saved);
  const full = [...new Set(currentKeys.filter((k) => !savedSet.has(k))), ...saved];
  const next = full.filter((k) => k !== key);
  const targetIndex = next.indexOf(targetKey);
  if (targetIndex === -1) return null;
  next.splice(placement === 'before' ? targetIndex : targetIndex + 1, 0, key);
  return next.every((k, index) => k === full[index]) ? null : next;
}

// Always returns a sections list so the JSX renders one shape — ungrouped is a
// single headerless section. Groups keep the flat list's recency order:
// date and workspace groups appear in order of their most recent chat (for
// workspaces, until the user drags them into an order of their own); status
// groups follow badge urgency. Pinned stays flat — it's a small, curated list.
export function buildRecentChatSections({
  groupBy,
  visibleRecentChats,
  workspaceBadgeById,
  blockedChatIdSet,
  streamingChatIdSet,
  completedChatIds,
  folderOrder,
}: GroupingContext): SidebarChatSection[] {
  if (groupBy === 'date') {
    const groups = new Map<number, SidebarChatSection>();
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);

    for (const chat of visibleRecentChats) {
      const chatDate = new Date(chat.updated_at);
      chatDate.setHours(0, 0, 0, 0);
      const key = chatDate.getTime();
      let group = groups.get(key);
      if (!group) {
        const label =
          key === today.getTime()
            ? 'Today'
            : key === yesterday.getTime()
              ? 'Yesterday'
              : formatDayLabel(chatDate, today);
        group = { key: String(key), label, chats: [] };
        groups.set(key, group);
      }
      group.chats.push(chat);
    }
    return Array.from(groups.values());
  }
  if (groupBy === 'workspace') {
    const groups = new Map<string, SidebarChatSection>();
    for (const chat of visibleRecentChats) {
      let group = groups.get(chat.workspace_id);
      if (!group) {
        group = {
          key: chat.workspace_id,
          label: chat.no_project
            ? 'No project'
            : (workspaceBadgeById.get(chat.workspace_id)?.name ?? 'Unknown project'),
          chats: [],
        };
        groups.set(chat.workspace_id, group);
      }
      group.chats.push(chat);
    }
    return sortByFolderOrder(Array.from(groups.values()), ({ key }) => key, folderOrder);
  }
  if (groupBy === 'status') {
    const byStatus = new Map<SidebarStatusFilter | 'other', Chat[]>();
    for (const chat of visibleRecentChats) {
      const status = blockedChatIdSet.has(chat.id)
        ? 'needs-you'
        : streamingChatIdSet.has(chat.id)
          ? 'running'
          : completedChatIds.has(chat.id)
            ? 'done'
            : chat.unread
              ? 'unread'
              : 'other';
      const list = byStatus.get(status);
      if (list) list.push(chat);
      else byStatus.set(status, [chat]);
    }
    return STATUS_GROUP_ORDER.flatMap(({ key, label }) => {
      const chats = byStatus.get(key);
      return chats ? [{ key, label, chats }] : [];
    });
  }
  return [{ key: 'all', label: null, chats: visibleRecentChats }];
}
