import { useState, type ReactNode } from 'react';
import clsx from 'clsx';
import { ChevronRight } from 'lucide-react';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { Button } from '@/components/ui/primitives/Button/Button';
import styles from './StepGroup.module.scss';

interface StepGroupProps {
  label: string;
  // The run is still growing: the header spins and, while folded, names the
  // newest step on the same line so live work stays visible without the layout
  // shifting as steps come and go.
  isLive: boolean;
  action?: ReactNode;
  children: ReactNode;
}

// A run of tool calls and thoughts behind one header; open on demand.
export function StepGroup({ label, isLive, action, children }: StepGroupProps) {
  const [isExpanded, setIsExpanded] = useState(false);
  const showAction = isLive && !isExpanded && action;
  return (
    <div className={styles['step-group']}>
      <Button
        type="button"
        variant="unstyled"
        onClick={() => setIsExpanded(!isExpanded)}
        aria-expanded={isExpanded}
        className={styles['step-button']}
      >
        {isLive && <AsciiSpinner className={styles['step-spinner']} />}
        <span className={styles['step-label']}>{label}</span>
        {showAction && <span className={styles['step-action']}>{action}</span>}
        <ChevronRight
          aria-hidden="true"
          className={clsx(styles['step-chevron'], isExpanded && styles['step-chevron--expanded'])}
        />
      </Button>
      {isExpanded && <div className={styles['step-content']}>{children}</div>}
    </div>
  );
}
