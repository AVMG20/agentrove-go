import { useId } from 'react';
import clsx from 'clsx';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { useDropdown } from '@/hooks/useDropdown';
import { getContextUsageDisplay, type ContextUsageInfo } from '@/utils/contextUsage';
import styles from './ComposerUsage.module.scss';

const RADIUS = 9;
const CIRCUMFERENCE = 2 * Math.PI * RADIUS;

// The context window as a small ring in the composer's top-right corner,
// tinted green → amber → red as it fills. Hover for the percentage, click for
// the token count.
export function ComposerUsage({ contextUsage }: { contextUsage: ContextUsageInfo }) {
  const { isOpen, setIsOpen, dropdownRef } = useDropdown();
  const panelId = useId();
  const context = getContextUsageDisplay(contextUsage);
  const summary = `Context ${context.label} used`;
  const dashOffset = CIRCUMFERENCE * (1 - context.percentage / 100);

  return (
    <div ref={dropdownRef} className={styles.usage}>
      <FloatingTooltip content={isOpen ? '' : summary} className={styles.tooltip}>
        <button
          type="button"
          className={styles.trigger}
          onClick={() => setIsOpen((open) => !open)}
          aria-expanded={isOpen}
          aria-controls={panelId}
          aria-label={summary}
        >
          <svg viewBox="0 0 24 24" className={styles.ring} aria-hidden="true">
            <circle cx="12" cy="12" r={RADIUS} className={styles.track} />
            <circle
              cx="12"
              cy="12"
              r={RADIUS}
              className={clsx(styles.progress, styles[`tone--${context.tone}`])}
              strokeDasharray={CIRCUMFERENCE}
              strokeDashoffset={dashOffset}
              transform="rotate(-90 12 12)"
            />
          </svg>
        </button>
      </FloatingTooltip>

      {isOpen && (
        <div id={panelId} className={styles.panel} role="dialog" aria-label="Context window">
          <div className={styles.heading}>
            <span>Context window</span>
            <span className={clsx(styles.figure, styles[`tone--${context.tone}`])}>
              {context.label}
            </span>
          </div>
          <p className={styles.detail}>{context.tooltip}</p>
        </div>
      )}
    </div>
  );
}
