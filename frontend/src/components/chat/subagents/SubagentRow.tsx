import clsx from 'clsx';
import { Check, Circle, Square, X } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import type { Subagent, SubagentStatus } from '@/types/chat.types';
import { SUBAGENT_STATUS_LABEL, subagentDuration, subagentStatusLabel } from '@/utils/subagents';
import styles from './Subagents.module.scss';

export function SubagentStatusIcon({ status }: { status: SubagentStatus }) {
  const Icon = { running: Circle, completed: Check, failed: X, stopped: Square }[status];
  return (
    <Icon
      className={clsx(styles['status-icon'], styles[`status-icon--${status}`])}
      aria-label={SUBAGENT_STATUS_LABEL[status]}
    />
  );
}

interface SubagentRowProps {
  subagent: Subagent;
  // Display name of the model it runs on, shown under its name.
  model?: string;
  now: number;
  selected: boolean;
  onSelect: () => void;
}

export function SubagentRow({ subagent, model, now, selected, onSelect }: SubagentRowProps) {
  const title = subagent.description || subagent.subagent_type;
  const meta = [model, subagent.last_activity].filter(Boolean).join(' · ');
  return (
    <Button
      variant="unstyled"
      type="button"
      className={clsx(styles.row, selected && styles['row--selected'])}
      onClick={onSelect}
      aria-pressed={selected}
      aria-label={`${title}, ${subagentStatusLabel(subagent).toLowerCase()}`}
    >
      <span className={styles['row-icon']}>
        <SubagentStatusIcon status={subagent.status} />
      </span>
      <span className={styles['row-body']}>
        <span className={styles['row-title']}>{title}</span>
        {meta && <span className={styles['row-activity']}>{meta}</span>}
      </span>
      <span className={styles['row-time']}>{subagentDuration(subagent, now)}</span>
    </Button>
  );
}
