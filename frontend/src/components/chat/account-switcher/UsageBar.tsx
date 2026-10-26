import clsx from 'clsx';
import type { UsageTone } from '@/utils/planUsage';
import styles from './AccountSwitcher.module.scss';

// A thin meter: light fill while there's room, amber then red as it runs out.
export function UsageBar({
  used,
  tone,
  label,
  className,
}: {
  used: number;
  tone: UsageTone;
  // Without one the bar is decorative (its numbers are in the text beside it).
  label?: string;
  className?: string;
}) {
  const rounded = Math.round(used);
  return (
    <span
      className={clsx(styles.bar, className)}
      {...(label
        ? {
            role: 'meter',
            'aria-label': label,
            'aria-valuemin': 0,
            'aria-valuemax': 100,
            'aria-valuenow': rounded,
          }
        : { 'aria-hidden': true })}
    >
      <span className={clsx(styles.fill, styles[`fill--${tone}`])} style={{ width: `${used}%` }} />
    </span>
  );
}
