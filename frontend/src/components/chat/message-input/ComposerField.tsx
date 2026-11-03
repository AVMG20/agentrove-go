import type { DragEvent, FormEvent, ReactNode, Ref } from 'react';
import clsx from 'clsx';
import { stateClasses } from '@/config/stateClasses';
import styles from './ComposerField.module.scss';

interface ComposerFieldProps {
  ref?: Ref<HTMLFormElement>;
  onSubmit: (e: FormEvent<HTMLFormElement>) => void;
  textarea: ReactNode;
  actions: ReactNode;
  // Start of the action row (attach), opposite the send button.
  leadingActions?: ReactNode;
  attachments?: ReactNode;
  // Pinned to the field's top-right corner (the usage ring).
  corner?: ReactNode;
  // The controls row under the field (account, model, effort, mode). On
  // desktop it is the card's bottom tray; on phones a row below the field.
  footer?: ReactNode;
  isDragging?: boolean;
  dragHandlers?: Record<string, (e: DragEvent<HTMLElement>) => void>;
  children?: ReactNode;
}

export function ComposerField({
  ref,
  onSubmit,
  textarea,
  actions,
  leadingActions,
  attachments,
  corner,
  footer,
  isDragging = false,
  dragHandlers,
  children,
}: ComposerFieldProps) {
  return (
    <form ref={ref} onSubmit={onSubmit} className={styles.composer}>
      <div className={clsx(styles.card, footer != null && styles['card--with-footer'])}>
        <div {...dragHandlers} className={clsx(styles.field, isDragging && stateClasses.DRAGGING)}>
          {corner && <div className={styles.corner}>{corner}</div>}
          {attachments}
          <div className={clsx(styles['textarea-wrap'], corner && styles['textarea-wrap--corner'])}>
            {textarea}
          </div>
          <div className={styles.actions}>
            {leadingActions && <div className={styles['actions-leading']}>{leadingActions}</div>}
            <div className={styles['actions-trailing']}>{actions}</div>
          </div>
        </div>
        {footer != null && <div className={styles.footer}>{footer}</div>}
      </div>
      {children}
    </form>
  );
}
