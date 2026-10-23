import { useId } from 'react';
import clsx from 'clsx';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { useDropdown } from '@/hooks/useDropdown';
import { useClaudeUsage } from '@/hooks/queries/useUsageQueries';
import { getContextUsageDisplay, type ContextUsageInfo } from '@/utils/contextUsage';
import { clampPercent, usageTone } from '@/utils/claudeUsage';
import type { ClaudeUsage } from '@/types/usage.types';
import { ClaudeUsageMeter } from './ClaudeUsageMeter';
import styles from './ComposerUsage.module.scss';

const RADIUS = 9;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

// The tighter of the two plan windows, or null when there's nothing to show.
function planPercent(usage: ClaudeUsage | undefined): number | null {
  if (!usage || usage.status !== 'ok') return null;
  const windows = [usage.session, usage.weekly].filter((w) => w != null);
  if (windows.length === 0) return null;
  return Math.max(...windows.map((w) => clampPercent(w.used_percent)));
}

// Context and plan usage, folded into one ring in the composer's top-right
// corner. The ring is the context window (green → amber → red); a dot on it
// flags a plan limit running low. Hover for a summary, click for the details
// and the per-window bars.
export function ComposerUsage({ contextUsage }: { contextUsage: ContextUsageInfo | null }) {
  const { usage } = useClaudeUsage();
  const { isOpen, setIsOpen, dropdownRef } = useDropdown();
  const panelId = useId();

  const context = contextUsage ? getContextUsageDisplay(contextUsage) : null;
  const plan = planPercent(usage);
  const showPlan = usage != null && usage.status !== 'unavailable';
  if (!context && !showPlan) return null;

  const planTone = plan == null ? null : usageTone(plan);
  const summary = [
    context ? `Context ${context.label}` : null,
    plan != null ? `Plan ${Math.round(plan)}% used` : null,
  ]
    .filter(Boolean)
    .join(' · ');
  const dashOffset = CIRCUMFERENCE * (1 - (context?.percentage ?? 0) / 100);

  return (
    <div ref={dropdownRef} className={styles.usage}>
      <FloatingTooltip content={isOpen ? '' : summary || 'Usage'} className={styles.tooltip}>
        <button
          type="button"
          className={styles.trigger}
          onClick={() => setIsOpen((open) => !open)}
          aria-expanded={isOpen}
          aria-controls={panelId}
          aria-label={summary ? `Usage: ${summary}` : 'Usage'}
        >
          <svg viewBox="0 0 24 24" className={styles.ring} aria-hidden="true">
            <circle cx="12" cy="12" r={RADIUS} className={styles.track} />
            {context && (
              <circle
                cx="12"
                cy="12"
                r={RADIUS}
                className={clsx(styles.progress, styles[`tone--${context.tone}`])}
                strokeDasharray={CIRCUMFERENCE}
                strokeDashoffset={dashOffset}
                transform="rotate(-90 12 12)"
              />
            )}
          </svg>
          {planTone && planTone !== 'normal' && (
            <span className={clsx(styles.flag, styles[`flag--${planTone}`])} aria-hidden="true" />
          )}
        </button>
      </FloatingTooltip>

      {isOpen && (
        <div id={panelId} className={styles.panel} role="dialog" aria-label="Usage">
          {context && (
            <section className={styles.section}>
              <div className={styles.heading}>
                <span>Context window</span>
                <span className={clsx(styles.figure, styles[`text--${context.tone}`])}>
                  {context.label}
                </span>
              </div>
              <p className={styles.detail}>{context.tooltip}</p>
            </section>
          )}
          {showPlan && (
            <section className={styles.section}>
              <div className={styles.heading}>
                <span>Plan usage</span>
              </div>
              <ClaudeUsageMeter />
            </section>
          )}
        </div>
      )}
    </div>
  );
}
