import { memo } from 'react';
import { PanelLeft, FileCode2 } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { EmptyState as BaseEmptyState } from '@/components/ui/shared/EmptyState/EmptyState';
import styles from './EmptyState.module.scss';

export interface EmptyStateProps {
  theme: string;
  onToggleFileTree?: () => void;
  isFileTreeCollapsed?: boolean;
}

export const EmptyState = memo(function EmptyState({
  // `theme` kept on the public API; surface token is theme-aware now.
  onToggleFileTree,
  isFileTreeCollapsed = false,
}: EmptyStateProps) {
  return (
    <div className={styles['empty-state']}>
      {/* Reopen affordance only — closing happens from the FILES panel header. */}
      {onToggleFileTree && isFileTreeCollapsed && (
        <div className={styles['empty-state-header']}>
          <Button
            variant="unstyled"
            onClick={onToggleFileTree}
            className={styles['toggle-tree']}
            aria-label="Open file tree"
          >
            <PanelLeft size={14} />
          </Button>
        </div>
      )}
      <BaseEmptyState
        icon={FileCode2}
        title="Select a file to edit"
        description="Open one from the file tree to view or change it."
      >
        {onToggleFileTree && isFileTreeCollapsed && (
          <Button variant="outline" size="sm" onClick={onToggleFileTree}>
            <PanelLeft size={14} />
            Show files
          </Button>
        )}
      </BaseEmptyState>
    </div>
  );
});
