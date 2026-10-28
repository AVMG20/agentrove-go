import type { MouseEvent, ReactNode } from 'react';
import clsx from 'clsx';
import { ChevronRight, MoreHorizontal, Plus } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useUIStore } from '@/store/uiStore';
import { folderColorClass, folderIcon } from './folderAppearance';
import type { FolderDragProps } from './useFolderDrag';
import styles from './SidebarWorkspaceGroup.module.scss';

interface SidebarWorkspaceGroupProps {
  workspaceId: string;
  name: string;
  // Folder presets (folderAppearance.ts); absent is the plain folder.
  icon?: string | null;
  color?: string | null;
  // Threads without a project: new threads start without one too, and the
  // scratch workspace has no project menu.
  noProject?: boolean;
  onNewThread: (e: MouseEvent, workspaceId: string | null) => void;
  onOpenMenu: (e: MouseEvent<HTMLButtonElement>, workspaceId: string) => void;
  // Reordering by dragging the header (useFolderDrag).
  drag?: FolderDragProps;
  children: ReactNode;
}

// A workspace folder in the threads list: collapsible, with new-thread and
// workspace-menu actions on hover. Rows inside drop their workspace subtitle.
export function SidebarWorkspaceGroup({
  workspaceId,
  name,
  icon,
  color,
  noProject = false,
  onNewThread,
  onOpenMenu,
  drag,
  children,
}: SidebarWorkspaceGroupProps) {
  const collapseKey = `workspace:${workspaceId}`;
  const collapsed = useUIStore((state) => state.sidebarCollapsed.includes(collapseKey));
  const FolderIcon = folderIcon(icon);

  return (
    <div
      className={clsx(
        styles.group,
        drag?.dragging && styles['group--dragging'],
        drag?.dropPlacement && styles[`group--drop-${drag.dropPlacement}`],
      )}
      onDragOver={drag?.onDragOver}
      onDragLeave={drag?.onDragLeave}
      onDrop={drag?.onDrop}
    >
      <div
        className={styles.header}
        draggable={!!drag}
        onDragStart={drag?.onDragStart}
        onDragEnd={drag?.onDragEnd}
      >
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={!collapsed}
          onClick={() => useUIStore.getState().toggleSidebarCollapsed(collapseKey)}
        >
          <span className={styles['icon-slot']}>
            <FolderIcon
              className={clsx(styles.icon, styles['icon--folder'], folderColorClass(color))}
            />
            <ChevronRight
              className={clsx(
                styles.icon,
                styles['icon--chevron'],
                !collapsed && styles['icon--expanded'],
              )}
            />
          </span>
          <span className={styles.name}>{name}</span>
        </button>
        <div className={styles.actions}>
          <Button
            variant="unstyled"
            type="button"
            onClick={(e) => onNewThread(e, noProject ? null : workspaceId)}
            className={styles.action}
            aria-label={noProject ? 'New thread without a project' : `New thread in ${name}`}
          >
            <Plus className={styles['action-icon']} />
          </Button>
          {!noProject && (
            <Button
              variant="unstyled"
              type="button"
              data-ws-dropdown-trigger
              onClick={(e) => onOpenMenu(e, workspaceId)}
              className={styles.action}
              aria-label={`${name} project options`}
            >
              <MoreHorizontal className={styles['action-icon']} />
            </Button>
          )}
        </div>
      </div>
      {!collapsed && <div className={styles.body}>{children}</div>}
    </div>
  );
}
