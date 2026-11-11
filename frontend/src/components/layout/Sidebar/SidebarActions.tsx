import { Search, SquarePen } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import {
  COMMAND_MENU_SHORTCUT_LABEL,
  commandShortcutLabel,
} from '@/components/ui/command-menu/commandRegistry';
import styles from './SidebarActions.module.scss';

interface SidebarActionsProps {
  onNewChat: () => void;
  onOpenSearch: () => void;
}

const NEW_THREAD_SHORTCUT = commandShortcutLabel('new-thread');

export function SidebarActions({ onNewChat, onOpenSearch }: SidebarActionsProps) {
  return (
    <nav className={styles.actions} aria-label="Primary">
      <Button
        onClick={onNewChat}
        variant="unstyled"
        className={clsx(styles.action, styles['action--primary'])}
      >
        <SquarePen className={styles['action-icon']} />
        <span className={styles['action-label']}>New thread</span>
        {NEW_THREAD_SHORTCUT && <kbd className={styles.kbd}>{NEW_THREAD_SHORTCUT}</kbd>}
      </Button>
      <Button onClick={onOpenSearch} variant="unstyled" className={styles.action}>
        <Search className={styles['action-icon']} />
        <span className={styles['action-label']}>Search</span>
        <kbd className={styles.kbd}>{COMMAND_MENU_SHORTCUT_LABEL}</kbd>
      </Button>
    </nav>
  );
}
