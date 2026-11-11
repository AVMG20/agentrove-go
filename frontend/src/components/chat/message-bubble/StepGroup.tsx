import { useState, type ReactNode } from 'react';
import clsx from 'clsx';
import { ChevronRight } from 'lucide-react';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { Button } from '@/components/ui/primitives/Button/Button';
import styles from './StepGroup.module.scss';

interface StepGroupProps {
  label: string;
  // The run is still growing: the header spins and, while folded, the newest
  // step stays in view so live work is never hidden.
  isLive: boolean;
  latest: ReactNode;
  children: ReactNode;
}

// A run of tool calls and thoughts behind one header; open on demand.
export function StepGroup({ label, isLive, latest, children }: StepGroupProps) {
  const [isExpanded, setIsExpanded] = useState(false);
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
        <span>{label}</span>
        <ChevronRight
          aria-hidden="true"
          className={clsx(styles['step-chevron'], isExpanded && styles['step-chevron--expanded'])}
        />
      </Button>
      {isExpanded ? (
        <div className={styles['step-content']}>{children}</div>
      ) : (
        isLive && <div className={styles['step-latest']}>{latest}</div>
      )}
    </div>
  );
}
