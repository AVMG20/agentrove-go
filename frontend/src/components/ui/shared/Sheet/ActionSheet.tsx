import { useEffect, useRef, useState, type ReactNode, type Ref } from 'react';
import { createPortal } from 'react-dom';
import clsx from 'clsx';
import { SheetHandle } from './SheetHandle';
import styles from './Sheet.module.scss';

interface ActionSheetProps {
  onClose: () => void;
  // Names the sheet (e.g. the thread a menu acts on); also its accessible name.
  title?: ReactNode;
  subtitle?: ReactNode;
  // Accessible name when there's no visible title.
  'aria-label'?: string;
  role?: 'menu' | 'dialog';
  // Extra class on the scrolling body, for the caller's row styles.
  bodyClassName?: string;
  // A separate Cancel button under the actions, as on iOS.
  showCancel?: boolean;
  // Lets callers treat taps inside the sheet as "inside" (click-outside checks).
  ref?: Ref<HTMLDivElement>;
  children: ReactNode;
}

const SCRIM_GUARD_MS = 400;

function assignRef<T>(ref: Ref<T> | undefined, value: T | null) {
  if (typeof ref === 'function') ref(value);
  else if (ref) (ref as { current: T | null }).current = value;
}

// A phone action sheet: portalled to <body> over a scrim, floating above the
// home indicator, with a grab handle (drag down to close), finger-sized rows
// and a Cancel button. Escape, the scrim and Cancel all close it.
export function ActionSheet({
  onClose,
  title,
  subtitle,
  'aria-label': ariaLabel,
  role = 'menu',
  bodyClassName,
  showCancel = true,
  ref,
  children,
}: ActionSheetProps) {
  const sheetRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  // A sheet opened by a long press appears under the finger; the click that
  // lifting it sends must not land on the scrim and close the sheet again.
  const [openedAt] = useState(() => Date.now());
  const handleScrimClick = () => {
    if (Date.now() - openedAt > SCRIM_GUARD_MS) onClose();
  };

  useEffect(() => {
    const previouslyFocused =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    sheetRef.current?.focus({ preventScroll: true });
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      e.preventDefault();
      e.stopPropagation();
      onCloseRef.current();
    };
    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      document.removeEventListener('keydown', handleKeyDown, true);
      if (previouslyFocused?.isConnected) previouslyFocused.focus({ preventScroll: true });
    };
  }, []);

  return createPortal(
    // Stop mousedown here so outside-click listeners on the opener (React events
    // bubble through portals) don't see taps inside the sheet as outside ones.
    <div className={styles.layer} onMouseDown={(e) => e.stopPropagation()}>
      <div className={styles.scrim} onClick={handleScrimClick} aria-hidden />
      <div
        ref={(el) => {
          sheetRef.current = el;
          assignRef(ref, el);
        }}
        role={role}
        aria-modal="true"
        aria-label={ariaLabel ?? (typeof title === 'string' ? title : undefined)}
        tabIndex={-1}
        className={styles['action-sheet']}
      >
        <div className={styles.group}>
          <SheetHandle sheetRef={sheetRef} onDismiss={onClose} />
          {(title || subtitle) && (
            <div className={styles.header}>
              {title && <div className={styles.title}>{title}</div>}
              {subtitle && <div className={styles.subtitle}>{subtitle}</div>}
            </div>
          )}
          <div className={clsx(styles.body, bodyClassName)}>{children}</div>
        </div>
        {showCancel && (
          <button type="button" className={styles.cancel} onClick={onClose}>
            Cancel
          </button>
        )}
      </div>
    </div>,
    document.body,
  );
}
