import { GitBranch, Box, Check, MessageSquare } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import type { Workspace } from '@/types/workspace.types';
import { formatRelativeTime } from '@/utils/date';
import styles from './WorkspaceItem.module.scss';

function sourceIcon(sourceType: string | null | undefined) {
  switch (sourceType) {
    case 'git':
      return <GitBranch className={styles['source-icon']} aria-hidden="true" />;
    default:
      return <Box className={styles['source-icon']} aria-hidden="true" />;
  }
}

function sourceLabel(sourceType: string | null | undefined) {
  switch (sourceType) {
    case 'git':
      return 'Git repository';
    default:
      return 'Empty project';
  }
}

// Threads without a project: just a chat with the model, no files or git.
export function NoProjectItem({
  isSelected,
  onSelect,
}: {
  isSelected: boolean;
  onSelect: () => void;
}) {
  return (
    <Button
      variant="unstyled"
      type="button"
      onClick={onSelect}
      aria-pressed={isSelected}
      className={clsx(styles.item, styles[isSelected ? 'item--selected' : 'item--unselected'])}
    >
      <MessageSquare className={styles['source-icon']} aria-hidden="true" />
      <div className={styles.content}>
        <div className={styles['name-row']}>
          <span className={styles.name}>No project</span>
          {isSelected && <Check className={styles['selected-icon']} aria-hidden="true" />}
        </div>
        <div className={styles['meta-row']}>
          <span>Just chat</span>
        </div>
      </div>
    </Button>
  );
}

export function WorkspaceItem({
  ws,
  isSelected,
  onSelect,
}: {
  ws: Workspace;
  isSelected: boolean;
  onSelect: (ws: Workspace) => void;
}) {
  return (
    <Button
      variant="unstyled"
      type="button"
      onClick={() => onSelect(ws)}
      aria-pressed={isSelected}
      className={clsx(styles.item, styles[isSelected ? 'item--selected' : 'item--unselected'])}
    >
      {sourceIcon(ws.source_type)}
      <div className={styles.content}>
        <div className={styles['name-row']}>
          <span className={styles.name}>{ws.name}</span>
          {isSelected && <Check className={styles['selected-icon']} aria-hidden="true" />}
        </div>
        <div className={styles['meta-row']}>
          <span>{sourceLabel(ws.source_type)}</span>
          <span aria-hidden="true">·</span>
          <span>{formatRelativeTime(ws.updated_at)}</span>
          {ws.chat_count > 0 && (
            <>
              <span aria-hidden="true">·</span>
              <span>
                {ws.chat_count} {ws.chat_count === 1 ? 'chat' : 'chats'}
              </span>
            </>
          )}
        </div>
      </div>
    </Button>
  );
}
