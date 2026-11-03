import { useCallback, useMemo } from 'react';
import { Archive, ArchiveRestore } from 'lucide-react';
import { Link } from 'react-router-dom';
import toast from 'react-hot-toast';
import { ListManagementTab } from '@/components/ui/ListManagementTab/ListManagementTab';
import { Button } from '@/components/ui/primitives/Button/Button';
import {
  useArchiveChatMutation,
  useArchivedChatsQuery,
  useDeleteChatMutation,
} from '@/hooks/queries/useChatQueries';
import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import { CHAT_ARCHIVE_DAYS, type Chat } from '@/types/chat.types';
import { formatRelativeTime } from '@/utils/date';
import { stripMarkdownTitle } from '@/utils/format';
import styles from './ArchivedSettingsTab.module.scss';

const DAY_MS = 24 * 60 * 60 * 1000;

// Whole days until the server deletes a chat archived at archivedAt.
function daysUntilPurge(archivedAt: string, now: number = Date.now()): number {
  const purgeAt = Date.parse(archivedAt) + CHAT_ARCHIVE_DAYS * DAY_MS;
  return Math.max(0, Math.ceil((purgeAt - now) / DAY_MS));
}

export function ArchivedSettingsTab() {
  const pages = useArchivedChatsQuery();
  const archiveChat = useArchiveChatMutation();
  const deleteChat = useDeleteChatMutation();
  const workspaces = useWorkspacesList();
  const items = useMemo(
    () => (pages.data ? pages.data.pages.flatMap((page) => page.items) : null),
    [pages.data],
  );
  const workspaceName = useMemo(
    () => new Map(workspaces.map((workspace) => [workspace.id, workspace.name])),
    [workspaces],
  );

  const handleRestore = useCallback(
    async (chat: Chat) => {
      try {
        await archiveChat.mutateAsync({ chatId: chat.id, archived: false });
        toast.success('Thread restored');
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to restore thread');
      }
    },
    [archiveChat],
  );

  const handleDelete = useCallback(
    async (index: number) => {
      const chat = items?.[index];
      if (!chat) return;
      try {
        await deleteChat.mutateAsync(chat.id);
        toast.success('Thread deleted');
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to delete thread');
        throw error;
      }
    },
    [items, deleteChat],
  );

  return (
    <ListManagementTab<Chat>
      title="Archived threads"
      description={`Threads you archived, hidden from the sidebar. Restore one to bring it back, or send it a message. Each is deleted ${CHAT_ARCHIVE_DAYS} days after it was archived.`}
      items={items}
      emptyIcon={Archive}
      emptyText="No archived threads. Archive one from its menu or the icon that shows when you hover over it."
      footerContent={
        pages.hasNextPage && (
          <div className={styles['load-more']}>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => void pages.fetchNextPage()}
              isLoading={pages.isFetchingNextPage}
            >
              Load more
            </Button>
          </div>
        )
      }
      deleteConfirmTitle="Delete thread"
      deleteConfirmMessage={(chat) =>
        `Delete "${stripMarkdownTitle(chat.title)}" now? This can't be undone.`
      }
      getItemKey={(chat) => chat.id}
      onDelete={handleDelete}
      renderItem={(chat) => {
        const days = chat.archived_at ? daysUntilPurge(chat.archived_at) : null;
        return (
          <>
            <h3 className={styles['item-title']}>
              <Link to={`/chat/${chat.id}`} className={styles['title-link']}>
                {stripMarkdownTitle(chat.title)}
              </Link>
            </h3>
            <div className={styles['item-meta']}>
              <span>
                {chat.no_project ? 'No project' : (workspaceName.get(chat.workspace_id) ?? '')}
              </span>
              {chat.archived_at && (
                <span className={styles.muted}>
                  Archived {formatRelativeTime(chat.archived_at)}
                </span>
              )}
              {days !== null && (
                <span className={styles.muted}>
                  {days === 0 ? 'Deletes today' : `Deletes in ${days} day${days === 1 ? '' : 's'}`}
                </span>
              )}
            </div>
          </>
        );
      }}
      renderItemActions={(chat) => (
        <button
          type="button"
          onClick={() => void handleRestore(chat)}
          className={styles['icon-button']}
          aria-label="Restore"
          title="Restore to the sidebar"
        >
          <ArchiveRestore className={styles.icon} />
        </button>
      )}
      logContext="ArchivedSettingsTab"
    />
  );
}
