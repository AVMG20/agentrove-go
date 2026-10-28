import { type MouseEvent, type Ref } from 'react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { clearSidebarFilters, type SidebarFilters } from '@/store/sidebarFilters';
import type { Chat } from '@/types/chat.types';
import type { WorkspaceBadge } from '@/hooks/queries/useSidebarChatLists';
import { SidebarChatRow, type ChatRowProps } from './SidebarChatRow';
import { SidebarFilterMenu } from './SidebarFilterMenu';
import { SidebarSection } from './SidebarSection';
import { SidebarWorkspaceGroup } from './SidebarWorkspaceGroup';
import type { FolderDropPlacement, SidebarChatSection } from './sidebarGrouping';
import { useFolderDrag } from './useFolderDrag';
import styles from './SidebarChatList.module.scss';

interface SidebarChatListProps {
  scrollContainerRef: Ref<HTMLDivElement>;
  isLoadingChats: boolean;
  hasAnyContent: boolean;
  visiblePinnedChats: Chat[];
  visibleRecentChats: Chat[];
  hasVisibleContent: boolean;
  recentChatSections: SidebarChatSection[];
  rowProps: ChatRowProps;
  filters: SidebarFilters;
  onChangeFilters: (filters: SidebarFilters) => void;
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  hasMore: boolean;
  isFetchingMore: boolean;
  onLoadMore: () => void;
  onNewWorkspaceThread: (e: MouseEvent, workspaceId: string | null) => void;
  // A workspace folder dropped before or after another one.
  onMoveFolder: (key: string, targetKey: string, placement: FolderDropPlacement) => void;
}

export function SidebarChatList({
  scrollContainerRef,
  isLoadingChats,
  hasAnyContent,
  visiblePinnedChats,
  visibleRecentChats,
  hasVisibleContent,
  recentChatSections,
  rowProps,
  filters,
  onChangeFilters,
  workspaceBadgeById,
  hasMore,
  isFetchingMore,
  onLoadMore,
  onNewWorkspaceThread,
  onMoveFolder,
}: SidebarChatListProps) {
  const groupedByWorkspace = filters.groupBy === 'workspace';
  const folderDrag = useFolderDrag(onMoveFolder);

  return (
    <div ref={scrollContainerRef} className={styles.scroll}>
      {!hasAnyContent ? (
        isLoadingChats ? null : (
          <p className={styles.empty}>No chats yet</p>
        )
      ) : (
        <div>
          {visiblePinnedChats.length > 0 && (
            <SidebarSection sectionKey="pinned" title="Pinned">
              <div className={styles.rows}>
                {visiblePinnedChats.map((chat) => (
                  <SidebarChatRow
                    key={chat.id}
                    chat={chat}
                    rowProps={rowProps}
                    // Folder mode keeps every row single-line; the open chat's
                    // environment panel still names its workspace.
                    hideWorkspace={groupedByWorkspace}
                  />
                ))}
              </div>
            </SidebarSection>
          )}

          {/* Header always renders so the filter menu stays reachable when filters empty the list. */}
          <SidebarSection
            sectionKey="threads"
            title="Threads"
            actions={
              <SidebarFilterMenu
                filters={filters}
                onChange={onChangeFilters}
                workspaceBadgeById={workspaceBadgeById}
              />
            }
          >
            {visibleRecentChats.length > 0 ? (
              <div className={styles.rows}>
                {recentChatSections.map((section) =>
                  groupedByWorkspace ? (
                    <SidebarWorkspaceGroup
                      key={section.key}
                      workspaceId={section.key}
                      name={section.label ?? 'Unknown project'}
                      icon={workspaceBadgeById.get(section.key)?.icon}
                      color={workspaceBadgeById.get(section.key)?.color}
                      noProject={section.chats[0]?.no_project}
                      onNewThread={onNewWorkspaceThread}
                      onOpenMenu={rowProps.onOpenWorkspaceMenu}
                      drag={folderDrag(section.key)}
                    >
                      {section.chats.map((chat) => (
                        <SidebarChatRow
                          key={chat.id}
                          chat={chat}
                          rowProps={rowProps}
                          hideWorkspace
                        />
                      ))}
                    </SidebarWorkspaceGroup>
                  ) : (
                    <div key={section.key} className={section.label ? styles.section : undefined}>
                      {section.label && (
                        <div className={styles['group-label']}>{section.label}</div>
                      )}
                      {section.chats.map((chat) => (
                        <SidebarChatRow key={chat.id} chat={chat} rowProps={rowProps} />
                      ))}
                    </div>
                  ),
                )}
              </div>
            ) : !hasVisibleContent ? (
              // Filters only narrow loaded pages — Load more may still surface matches.
              <div className={styles['no-match']}>
                <p className={styles['no-match-text']}>No chats match the current filters</p>
                <Button
                  variant="unstyled"
                  onClick={() => onChangeFilters(clearSidebarFilters(filters))}
                  className={styles['clear-filters']}
                >
                  Clear filters
                </Button>
              </div>
            ) : null}
          </SidebarSection>
          {/* Outside Threads so Load more shows even when every loaded chat is filtered out. */}
          {hasMore && (
            <Button
              variant="unstyled"
              type="button"
              onClick={onLoadMore}
              disabled={isFetchingMore}
              className={styles['load-more']}
            >
              {isFetchingMore ? (
                <>
                  <Spinner size="xs" />
                  Loading…
                </>
              ) : (
                'Load more'
              )}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}
