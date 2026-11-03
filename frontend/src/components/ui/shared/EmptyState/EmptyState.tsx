import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';
import clsx from 'clsx';
import styles from './EmptyState.module.scss';

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description?: ReactNode;
  // Real buttons (e.g. Retry, Reconnect) under the text.
  children?: ReactNode;
  className?: string;
}

// The one look for a view with nothing to show (no file open, no changes, a
// dropped connection): an icon tile, a readable title, an optional line of
// explanation and, when there's a way forward, a button. Fills and centers in
// its parent.
export function EmptyState({
  icon: Icon,
  title,
  description,
  children,
  className,
}: EmptyStateProps) {
  return (
    <div className={clsx(styles['empty-state'], className)}>
      <span className={styles.tile} aria-hidden>
        <Icon className={styles.icon} />
      </span>
      <div className={styles.text}>
        <p className={styles.title}>{title}</p>
        {description && <p className={styles.description}>{description}</p>}
      </div>
      {children && <div className={styles.actions}>{children}</div>}
    </div>
  );
}
