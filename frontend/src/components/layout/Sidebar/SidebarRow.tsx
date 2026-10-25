import type { MouseEvent, ReactNode, Ref } from 'react';
import clsx from 'clsx';
import { MoreHorizontal } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { stateClasses } from '@/config/stateClasses';
import styles from './SidebarRow.module.scss';

interface SidebarRowProps {
  ref?: Ref<HTMLDivElement>;
  isActive: boolean;
  // Keep the row actions visible (dropdown open); hover/focus reveal them via CSS.
  isRevealed: boolean;
  // Clicks anywhere on the row outside its buttons, so the whole row opens it.
  onRowClick?: (e: MouseEvent<HTMLDivElement>) => void;
  leading?: ReactNode;
  title: ReactNode;
  subtitle?: ReactNode;
  timestamp: string;
  dropdownLabel: string;
  onDropdownClick: (e: MouseEvent<HTMLButtonElement>) => void;
}

export function SidebarRow({
  ref,
  isActive,
  isRevealed,
  onRowClick,
  leading,
  title,
  subtitle,
  timestamp,
  dropdownLabel,
  onDropdownClick,
}: SidebarRowProps) {
  return (
    // Keyboard users reach the same action through the title button.
    <div
      ref={ref}
      onClick={
        onRowClick &&
        ((e) => {
          if (!(e.target as HTMLElement).closest('button, a')) onRowClick(e);
        })
      }
      className={clsx(
        styles.row,
        isActive && stateClasses.ACTIVE,
        (isRevealed || isActive) && styles['row--revealed'],
        onRowClick && styles['row--clickable'],
      )}
    >
      {/* Pad for timestamp/dropdown so long titles truncate before them. */}
      <div className={styles['title-col']}>
        <div className={styles['title-row']}>
          {leading}
          {title}
        </div>
        {subtitle}
      </div>

      {/* Timestamp; hides on hover for the dropdown. Status is on the leading icon. */}
      <span className={styles.timestamp}>{timestamp}</span>

      <Button
        onClick={onDropdownClick}
        onMouseDown={(e) => e.stopPropagation()}
        variant="unstyled"
        className={styles['dropdown-btn']}
        aria-label={dropdownLabel}
      >
        <MoreHorizontal className={styles['dropdown-icon']} />
      </Button>
    </div>
  );
}

interface SidebarRowTitleProps {
  label: string;
  isEmphasized: boolean;
  isCurrent: boolean;
  // An agent turn is running in this thread.
  isWorking?: boolean;
  onClick: (e: MouseEvent<HTMLButtonElement>) => void;
}

export function SidebarRowTitle({
  label,
  isEmphasized,
  isCurrent,
  isWorking = false,
  onClick,
}: SidebarRowTitleProps) {
  return (
    <Button
      onClick={onClick}
      aria-current={isCurrent ? 'page' : undefined}
      variant="unstyled"
      className={styles['title-btn']}
    >
      <span
        className={clsx(
          styles['title-text'],
          isEmphasized && styles['title-text--emphasis'],
          isWorking && styles['title-text--working'],
        )}
      >
        {label}
      </span>
    </Button>
  );
}
