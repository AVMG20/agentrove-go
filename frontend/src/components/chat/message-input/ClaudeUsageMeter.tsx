import { useEffect, useState } from 'react';
import clsx from 'clsx';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { RefreshButton } from '@/components/ui/shared/RefreshButton/RefreshButton';
import { useClaudeUsage } from '@/hooks/queries/useUsageQueries';
import type { ClaudeUsageWindow } from '@/types/usage.types';
import {
  clampPercent,
  describeUsageWindow,
  formatResetIn,
  resetAtMs,
  usageTone,
} from '@/utils/claudeUsage';
import styles from './ClaudeUsageMeter.module.scss';

// Keeps the reset countdowns moving between polls.
function useNow(intervalMs: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

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
  const resetAt = resetAtMs(window);
  // The window rolled over since the last fetch; a refetch is already scheduled.
  const rolledOver = resetAt != null && resetAt <= now;
  const used = rolledOver ? 0 : clampPercent(window.used_percent);
  const resetLabel = resetAt != null && !rolledOver ? formatResetIn(resetAt - now) : null;
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
          className={clsx(styles.fill, styles[`fill--${usageTone(used)}`])}
          style={{ width: `${used}%` }}
        />
      </span>
      <span className={styles.value}>{Math.round(used)}%</span>
      {resetLabel && <span className={styles.reset}>{resetLabel}</span>}
    </FloatingTooltip>
  );
}

export function ClaudeUsageMeter({ className }: { className?: string }) {
  const { usage, isRefreshing, refresh } = useClaudeUsage();
  const now = useNow(30_000);

  if (!usage || usage.status === 'unavailable') return null;

  return (
    <div className={clsx(styles.meter, className)}>
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
      <RefreshButton
        onClick={() => void refresh()}
        isRefreshing={isRefreshing}
        title={usage.provider ? `Refresh usage (${usage.provider})` : 'Refresh usage'}
        ariaLabel="Refresh Claude usage"
      />
    </div>
  );
}
