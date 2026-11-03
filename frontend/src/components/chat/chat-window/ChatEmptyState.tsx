import { memo } from 'react';
import { MessageSquareText } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import styles from './ChatEmptyState.module.scss';

const STARTERS = [
  'Explain how this project is structured',
  'Find and fix a bug',
  'Write tests for recent changes',
];

interface ChatEmptyStateProps {
  // Fills the composer with a starter (doesn't send), so it can be edited first.
  onPickStarter: (text: string) => void;
}

// A thread with no messages yet: says what to do instead of showing a blank page.
export const ChatEmptyState = memo(function ChatEmptyState({ onPickStarter }: ChatEmptyStateProps) {
  return (
    <div className={styles['empty-state']}>
      <div className={styles.badge} aria-hidden>
        <MessageSquareText className={styles['badge-icon']} />
      </div>
      <h2 className={styles.title}>Start the conversation</h2>
      <p className={styles.hint}>
        Ask anything. Type @ to mention files or agents, / for commands.
      </p>
      <div className={styles.starters}>
        {STARTERS.map((text) => (
          <Button
            key={text}
            type="button"
            variant="unstyled"
            className={styles.starter}
            onClick={() => onPickStarter(text)}
          >
            {text}
          </Button>
        ))}
      </div>
    </div>
  );
});
