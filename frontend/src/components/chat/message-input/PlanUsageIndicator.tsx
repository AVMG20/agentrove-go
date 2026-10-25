import { useId } from 'react';
import clsx from 'clsx';
import { ClaudeIcon } from '@/components/ui/icons/ClaudeIcon';
import { useDropdown } from '@/hooks/useDropdown';
import { useClaudeUsage } from '@/hooks/queries/useUsageQueries';
import type { ClaudeUsageWindow } from '@/types/usage.types';
import { useNow } from '@/hooks/useNow';
import { summarizeWindow } from '@/utils/claudeUsage';
import { ClaudeUsageMeter } from './ClaudeUsageMeter';
import styles from './PlanUsageIndicator.module.scss';

function windowText(window: ClaudeUsageWindow | null, now: number): string | null {
  if (!window) return null;
  const { used, resetLabel } = summarizeWindow(window, now);
  return [`${Math.round(used)}%`, resetLabel].filter(Boolean).join(' ');
}

// Always-visible plan usage under the composer: the Claude spark, a thin bar
// for the 5-hour window, then "39% 4h 18m · 6% 6d 19h" (5-hour, then weekly,
// each with time to reset). Click for the per-window bars and a refresh.
export function PlanUsageIndicator({
  profileId,
  // Off when the account picker beside it already shows the provider icon.
  showSpark = true,
}: {
  profileId?: string;
  showSpark?: boolean;
}) {
  const { usage } = useClaudeUsage(profileId);
  const now = useNow(30_000);
  const { isOpen, setIsOpen, dropdownRef } = useDropdown();
  const panelId = useId();

  if (!usage || usage.status === 'unavailable') return null;

  const session = usage.session ? summarizeWindow(usage.session, now) : null;
  const text =
    usage.status === 'error'
      ? 'Usage unavailable'
      : [windowText(usage.session, now), windowText(usage.weekly, now)].filter(Boolean).join(' · ');

  return (
    <div ref={dropdownRef} className={styles.indicator}>
      <button
        type="button"
        className={styles.trigger}
        onClick={() => setIsOpen((open) => !open)}
        aria-expanded={isOpen}
        aria-controls={panelId}
        aria-label={`Claude plan usage: ${text}`}
        title={isOpen ? undefined : 'Plan usage (5-hour · weekly)'}
      >
        {showSpark && <ClaudeIcon aria-hidden className={styles.spark} />}
        {session && (
          <span className={styles.bar} aria-hidden="true">
            <span
              className={clsx(styles.fill, styles[`fill--${session.tone}`])}
              style={{ width: `${session.used}%` }}
            />
          </span>
        )}
        <span className={styles.text}>{text}</span>
      </button>

      {isOpen && (
        <div id={panelId} className={styles.panel} role="dialog" aria-label="Plan usage">
          <div className={styles.heading}>Plan usage</div>
          <ClaudeUsageMeter profileId={profileId} />
        </div>
      )}
    </div>
  );
}
