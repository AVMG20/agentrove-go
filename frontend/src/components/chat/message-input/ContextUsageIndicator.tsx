import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { getContextUsageDisplay, type ContextUsageInfo } from '@/utils/contextUsage';
import styles from './ContextUsageIndicator.module.scss';

const RADIUS = 9;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

export function ContextUsageIndicator({ usage }: { usage: ContextUsageInfo }) {
  const { percentage, tone, label, tooltip } = getContextUsageDisplay(usage);
  const dashOffset = CIRCUMFERENCE * (1 - percentage / 100);

  return (
    <FloatingTooltip content={tooltip} className={styles['context-usage']}>
      <span className={styles.value}>{label}</span>
      <svg viewBox="0 0 24 24" className={styles.ring} role="presentation" aria-hidden="true">
        <circle
          cx="12"
          cy="12"
          r={RADIUS}
          strokeWidth="2"
          stroke="currentColor"
          className={styles['ring-track']}
          fill="none"
        />
        <circle
          cx="12"
          cy="12"
          r={RADIUS}
          strokeWidth="2"
          stroke="currentColor"
          className={styles[`ring-progress--${tone}`]}
          fill="none"
          strokeDasharray={CIRCUMFERENCE}
          strokeDashoffset={dashOffset}
          strokeLinecap="round"
          transform="rotate(-90 12 12)"
        />
      </svg>
    </FloatingTooltip>
  );
}
