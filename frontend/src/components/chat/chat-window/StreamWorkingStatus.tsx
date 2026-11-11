import { memo, useRef } from 'react';
import { useNow } from '@/hooks/useNow';
import { formatDuration } from '@/utils/date';
import { StatusIndicator } from './StatusTypewriter';
import styles from './StatusTypewriter.module.scss';

// Wait for a real pause before showing the status under a quiet stream.
export const STREAM_IDLE_MS = 5000;

interface StreamWorkingStatusProps {
  streamStartTime?: number;
  // Changes whenever the streaming message grows or a tool event arrives.
  activityKey: string;
  // Assistant text ends the current tool/thinking group and clears the latched status.
  assistantTextIsActive: boolean;
}

// "Still working · 4m 12s" under a quiet streaming reply. After it appears, it
// stays through tool and thinking events until assistant text resumes. One 1s
// timer; renders from stream flushes only compare the activity key.
export const StreamWorkingStatus = memo(function StreamWorkingStatus({
  streamStartTime,
  activityKey,
  assistantTextIsActive,
}: StreamWorkingStatusProps) {
  const now = useNow(1000);
  const startRef = useRef(streamStartTime ?? now);
  if (streamStartTime !== undefined) startRef.current = streamStartTime;
  const lastActivityRef = useRef({ key: activityKey, at: now, shown: false });
  if (lastActivityRef.current.key !== activityKey) {
    lastActivityRef.current = {
      key: activityKey,
      at: Date.now(),
      shown: lastActivityRef.current.shown && !assistantTextIsActive,
    };
  }

  if (assistantTextIsActive) {
    lastActivityRef.current.shown = false;
    return null;
  }
  if (now - lastActivityRef.current.at >= STREAM_IDLE_MS) {
    lastActivityRef.current.shown = true;
  }
  if (!lastActivityRef.current.shown) return null;

  return (
    <StatusIndicator
      text="Still working"
      caretBlinking
      trailing={
        <span className={styles['status-elapsed']}>
          · {formatDuration(Math.max(0, now - startRef.current))}
        </span>
      }
    />
  );
});
