import { Search, SquarePen } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { formatShortcut } from '@/components/ui/command-menu/commandRegistry';
import styles from './SidebarActions.module.scss';

interface SidebarActionsProps {
  onNewChat: () => void;
  onOpenSearch: () => void;
}

export function SidebarActions({ onNewChat, onOpenSearch }: SidebarActionsProps) {
  return (
    <nav className={styles.actions} aria-label="Primary">
      <Button onClick={onNewChat} variant="unstyled" className={styles.action}>
        <SquarePen className={styles['action-icon']} />
        <span className={styles['action-label']}>New thread</span>
        <kbd className={styles.kbd}>{formatShortcut('w')}</kbd>
      </Button>
      <Button onClick={onOpenSearch} variant="unstyled" className={styles.action}>
        <Search className={styles['action-icon']} />
        <span className={styles['action-label']}>Search</span>
        <kbd className={styles.kbd}>{formatShortcut('p')}</kbd>
      </Button>
    </nav>
  );
}
