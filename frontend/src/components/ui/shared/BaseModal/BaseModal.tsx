import { ReactNode, useEffect, useRef } from 'react';
import { createPortal } from 'react-dom';
import clsx from 'clsx';
import styles from './BaseModal.module.scss';

export type ModalSize = 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '4xl' | 'full';
export type ModalZIndex = 'modal' | 'modalHigh' | 'modalHighest';

export interface BaseModalProps {
  isOpen: boolean;
  onClose: () => void;
  children: ReactNode;
  size?: ModalSize;
  // Retained for API compatibility; all tiers share the 'modal' z-layer and stack by
  // portal mount order (see BaseModal.module.scss).
  zIndex?: ModalZIndex;
  className?: string;
  // Accessible name: prefer `ariaLabelledBy` (id of the visible title); `ariaLabel`
  // is for dialogs without one.
  ariaLabelledBy?: string;
  ariaLabel?: string;
}

const TABBABLE_SELECTOR = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'iframe',
  '[contenteditable]:not([contenteditable="false"])',
  '[tabindex]',
].join(',');

function getTabbables(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(TABBABLE_SELECTOR)).filter(
    (el) => el.tabIndex >= 0 && !el.closest('[hidden],[inert]'),
  );
}

// Open modals in open order; only the last one handles Escape and traps Tab, so a
// ConfirmDialog over another dialog doesn't close both.
const openModals: object[] = [];

export function BaseModal({
  isOpen,
  onClose,
  children,
  size = 'md',
  className,
  ariaLabelledBy,
  ariaLabel,
}: BaseModalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  useEffect(() => {
    if (!isOpen) return;
    const container = containerRef.current;
    if (!container) return;

    const token = {};
    openModals.push(token);
    const previouslyFocused =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;

    // Children that focus themselves on mount (autoFocus inputs) keep their focus.
    if (!container.contains(document.activeElement)) {
      (getTabbables(container)[0] ?? container).focus();
    }

    // Document-level so Escape works wherever focus is; bubble phase so inner widgets
    // (menus, editors) that handle the key first can preventDefault/stopPropagation.
    const handleKeyDown = (e: KeyboardEvent) => {
      if (openModals[openModals.length - 1] !== token || e.defaultPrevented) return;

      if (e.key === 'Escape') {
        e.preventDefault();
        onCloseRef.current();
        return;
      }

      if (e.key !== 'Tab') return;
      const tabbables = getTabbables(container);
      if (tabbables.length === 0) {
        e.preventDefault();
        container.focus();
        return;
      }
      const first = tabbables[0];
      const last = tabbables[tabbables.length - 1];
      const active = document.activeElement;
      const outside = !container.contains(active) || active === container;
      if (e.shiftKey ? outside || active === first : outside || active === last) {
        e.preventDefault();
        (e.shiftKey ? last : first).focus();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      const index = openModals.indexOf(token);
      if (index !== -1) openModals.splice(index, 1);
      if (previouslyFocused?.isConnected) previouslyFocused.focus();
    };
  }, [isOpen]);

  if (!isOpen) return null;

  return createPortal(
    <div className={styles.backdrop} onClick={onClose} role="presentation">
      <div
        ref={containerRef}
        className={clsx(styles.container, styles[`container--${size}`], className)}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby={ariaLabelledBy}
        aria-label={ariaLabelledBy ? undefined : ariaLabel}
        tabIndex={-1}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}
