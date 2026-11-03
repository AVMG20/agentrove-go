import type { RefObject } from 'react';
import clsx from 'clsx';
import { useSheetDrag } from './useSheetDrag';
import styles from './Sheet.module.scss';

interface SheetHandleProps {
  // The sheet that moves with the finger.
  sheetRef: RefObject<HTMLElement | null>;
  onDismiss: () => void;
  className?: string;
}

// The grab bar on top of a phone bottom sheet: drag it down to close the sheet.
// Decorative for assistive tech, which closes sheets with Escape or Cancel.
export function SheetHandle({ sheetRef, onDismiss, className }: SheetHandleProps) {
  const drag = useSheetDrag(sheetRef, onDismiss);
  return (
    <div className={clsx(styles.handle, className)} aria-hidden {...drag}>
      <span className={styles['handle-pill']} />
    </div>
  );
}
