import type { MouseEvent, ReactNode } from 'react';
import clsx from 'clsx';
import { ChevronRight, Folder, MoreHorizontal, Plus } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useUIStore } from '@/store/uiStore';
import styles from './SidebarWorkspaceGroup.module.scss';

interface SidebarWorkspaceGroupProps {
  workspaceId: string;
  name: string;
  onNewThread: (e: MouseEvent, workspaceId: string) => void;
  onOpenMenu: (e: MouseEvent<HTMLButtonElement>, workspaceId: string) => void;
  children: ReactNode;
}

// A workspace folder in the threads list: collapsible, with new-thread and
// workspace-menu actions on hover. Rows inside drop their workspace subtitle.
export function SidebarWorkspaceGroup({
  workspaceId,
  name,
  onNewThread,
  onOpenMenu,
  children,
}: SidebarWorkspaceGroupProps) {
  const collapseKey = `workspace:${workspaceId}`;
  const collapsed = useUIStore((state) => state.sidebarCollapsed.includes(collapseKey));

  return (
    <div className={styles.group}>
      <div className={styles.header}>
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={!collapsed}
          onClick={() => useUIStore.getState().toggleSidebarCollapsed(collapseKey)}
        >
          <span className={styles['icon-slot']}>
            <Folder className={clsx(styles.icon, styles['icon--folder'])} />
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
            onClick={(e) => onNewThread(e, workspaceId)}
            className={styles.action}
            aria-label={`New thread in ${name}`}
          >
            <Plus className={styles['action-icon']} />
          </Button>
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
        </div>
      </div>
      {!collapsed && <div className={styles.body}>{children}</div>}
    </div>
  );
}
