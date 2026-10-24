import type { MouseEvent, ReactNode } from 'react';
import { BaseModal } from '@/components/ui/shared/BaseModal/BaseModal';
import styles from './SessionSheet.module.scss';

interface SessionSheetProps {
  isOpen: boolean;
  onClose: () => void;
  children: ReactNode;
}

// The session panel as a bottom sheet on phones, where it has no room beside
// the chat. Acting on anything in it (open a diff, a sub-thread, a PR) closes
// it, since that action's result is behind the sheet.
export function SessionSheet({ isOpen, onClose, children }: SessionSheetProps) {
  const handleClick = (event: MouseEvent) => {
    if ((event.target as Element).closest('a, button')) onClose();
  };

  return (
    <BaseModal
      isOpen={isOpen}
      onClose={onClose}
      size="full"
      className={styles.sheet}
      ariaLabel="Session details"
    >
      <div className={styles.handle} aria-hidden />
      <div className={styles.body} onClick={handleClick}>
        {children}
      </div>
    </BaseModal>
  );
}
