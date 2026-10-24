import { CircleAlert, CircleStop, Play, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import styles from './TurnStatusFooter.module.scss';

interface TurnStatusFooterProps {
  status: 'failed' | 'interrupted';
  // Failure reason; older failed messages carry it in their text instead.
  error?: string | null;
  onRetry?: () => void;
  onContinue: () => void;
}

// Marks a turn that didn't finish (stopped, failed, or cut off by a server
// restart) so it doesn't read like a normal answer, with ways to pick it up.
export function TurnStatusFooter({ status, error, onRetry, onContinue }: TurnStatusFooterProps) {
  const failed = status === 'failed';
  const Icon = failed ? CircleAlert : CircleStop;
  const label = failed ? (error ? `Failed: ${error}` : 'Failed') : 'Stopped';

  return (
    <div className={styles.footer} role="status">
      <span className={styles.label} title={label}>
        <Icon className={styles.icon} aria-hidden />
        <span className={styles.text}>{label}</span>
      </span>
      <div className={styles.actions}>
        {onRetry && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onRetry}
            className={styles.action}
          >
            <RotateCcw className={styles['action-icon']} />
            Retry
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onContinue}
          className={styles.action}
        >
          <Play className={styles['action-icon']} />
          Continue
        </Button>
      </div>
    </div>
  );
}
