import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { CHAT_ARCHIVE_DAYS } from '@/types/chat.types';
import styles from './archiveToast.module.scss';

// "Archived" with an Undo that restores the thread; lingers long enough to reach.
export function showArchivedToast(onUndo: () => void) {
  toast.success(
    (t) => (
      <span className={styles.toast}>
        <span title={`Archived threads are deleted after ${CHAT_ARCHIVE_DAYS} days`}>
          Thread archived
        </span>
        <Button
          variant="unstyled"
          className={styles.undo}
          onClick={() => {
            toast.dismiss(t.id);
            onUndo();
          }}
        >
          Undo
        </Button>
      </span>
    ),
    { duration: 5000 },
  );
}
