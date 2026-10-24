import { memo, useRef } from 'react';
import { useNow } from '@/hooks/useNow';
import { formatDuration } from '@/utils/date';
import { StatusIndicator } from './StatusTypewriter';
import styles from './StatusTypewriter.module.scss';

// Quiet this long before the row appears, so it never flickers between tokens.
export const STREAM_IDLE_MS = 2000;

interface StreamWorkingStatusProps {
  streamStartTime?: number;
  // Changes whenever the streaming message grows; any change resets the idle clock.
  activityKey: string;
}

// "Still working · 4m 12s" under a streaming reply that has gone quiet with no
// tool card running (long thinking, slow model). One 1s timer; renders from
// stream flushes are cheap: the key compare is all they do.
export const StreamWorkingStatus = memo(function StreamWorkingStatus({
  streamStartTime,
  activityKey,
}: StreamWorkingStatusProps) {
  const now = useNow(1000);
  const startRef = useRef(streamStartTime ?? now);
  if (streamStartTime !== undefined) startRef.current = streamStartTime;
  const lastActivityRef = useRef({ key: activityKey, at: now });
  if (lastActivityRef.current.key !== activityKey) {
    lastActivityRef.current = { key: activityKey, at: Date.now() };
  }

  if (now - lastActivityRef.current.at < STREAM_IDLE_MS) return null;

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
