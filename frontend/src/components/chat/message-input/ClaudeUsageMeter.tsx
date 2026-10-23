import clsx from 'clsx';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { RefreshButton } from '@/components/ui/shared/RefreshButton/RefreshButton';
import { useClaudeUsage } from '@/hooks/queries/useUsageQueries';
import type { ClaudeUsageWindow } from '@/types/usage.types';
import { describeUsageWindow, summarizeWindow } from '@/utils/claudeUsage';
import { useNow } from '@/hooks/useNow';
import styles from './ClaudeUsageMeter.module.scss';

function UsageWindow({
  label,
  name,
  window,
  now,
}: {
  label: string;
  name: string;
  window: ClaudeUsageWindow;
  now: number;
}) {
  const { used, rolledOver, resetLabel, tone } = summarizeWindow(window, now);
  return (
    <FloatingTooltip
      content={rolledOver ? `${name}: reset` : describeUsageWindow(name, window, now)}
      className={styles.window}
    >
      <span className={styles.label}>{label}</span>
      <span
        className={styles.bar}
        role="meter"
        aria-label={`${name} usage`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(used)}
        aria-valuetext={`${Math.round(used)}% used${resetLabel ? `, resets in ${resetLabel}` : ''}`}
      >
        <span
          className={clsx(styles.fill, styles[`fill--${tone}`])}
          style={{ width: `${used}%` }}
        />
      </span>
      <span className={styles.value}>{Math.round(used)}%</span>
      {resetLabel && <span className={styles.reset}>{resetLabel}</span>}
    </FloatingTooltip>
  );
}

// The plan windows (5-hour and weekly), one row each, with reset times and a
// refresh button: the body of the plan-usage popover under the composer.
export function ClaudeUsageMeter() {
  const { usage, isRefreshing, refresh } = useClaudeUsage();
  const now = useNow(30_000);

  if (!usage || usage.status === 'unavailable') return null;

  return (
    <div className={styles.meter}>
      {usage.status === 'error' ? (
        <FloatingTooltip content={usage.error ?? 'Usage unavailable'} className={styles.window}>
          <span className={styles.label}>Usage unavailable</span>
        </FloatingTooltip>
      ) : (
        <>
          {usage.session && (
            <UsageWindow label="5h" name="5-hour limit" window={usage.session} now={now} />
          )}
          {usage.weekly && (
            <UsageWindow label="Week" name="Weekly limit" window={usage.weekly} now={now} />
          )}
        </>
      )}
      <div className={styles.footer}>
        {usage.provider && <span className={styles.provider}>{usage.provider}</span>}
        <RefreshButton
          onClick={() => void refresh()}
          isRefreshing={isRefreshing}
          title="Refresh usage"
          ariaLabel="Refresh Claude usage"
        />
      </div>
    </div>
  );
}
