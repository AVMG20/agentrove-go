import type { DragEvent, FormEvent, ReactNode, Ref } from 'react';
import clsx from 'clsx';
import { stateClasses } from '@/config/stateClasses';
import styles from './ComposerField.module.scss';

interface ComposerFieldProps {
  ref?: Ref<HTMLFormElement>;
  onSubmit: (e: FormEvent<HTMLFormElement>) => void;
  textarea: ReactNode;
  actions: ReactNode;
  attachments?: ReactNode;
  // Pinned to the field's top-right corner (the usage ring).
  corner?: ReactNode;
  isDragging?: boolean;
  dragHandlers?: Record<string, (e: DragEvent<HTMLElement>) => void>;
  children?: ReactNode;
}

export function ComposerField({
  ref,
  onSubmit,
  textarea,
  actions,
  attachments,
  corner,
  isDragging = false,
  dragHandlers,
  children,
}: ComposerFieldProps) {
  return (
    <form ref={ref} onSubmit={onSubmit} className={styles.composer}>
      <div {...dragHandlers} className={clsx(styles.field, isDragging && stateClasses.DRAGGING)}>
        {corner && <div className={styles.corner}>{corner}</div>}
        {attachments}
        <div className={clsx(styles['textarea-wrap'], corner && styles['textarea-wrap--corner'])}>
          {textarea}
        </div>
        <div className={styles.actions}>{actions}</div>
      </div>
      {children}
    </form>
  );
}
