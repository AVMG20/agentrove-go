import { useCallback, useRef, type PointerEvent as ReactPointerEvent, type RefObject } from 'react';

// Past this many px, or flicked faster than this (px/ms), a released sheet closes.
const DISMISS_DISTANCE = 96;
const DISMISS_VELOCITY = 0.5;
// How long the sheet takes to slide away before onDismiss unmounts it.
const EXIT_MS = 180;

export interface SheetDragHandlers {
  onPointerDown: (e: ReactPointerEvent<HTMLElement>) => void;
}

// Drag a bottom sheet down by its handle to dismiss it, like a native sheet.
// Moves the sheet with the finger (transform only, no re-renders), springs it
// back on a short drag and slides it out on a long or fast one. Spread the
// returned handlers on the grab area; it needs touch-action: none.
export function useSheetDrag(
  sheetRef: RefObject<HTMLElement | null>,
  onDismiss: () => void,
): SheetDragHandlers {
  const onDismissRef = useRef(onDismiss);
  onDismissRef.current = onDismiss;

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLElement>) => {
      const sheet = sheetRef.current;
      // Primary button / single finger only.
      if (!sheet || (e.pointerType === 'mouse' && e.button !== 0) || !e.isPrimary) return;
      const handle = e.currentTarget;
      const pointerId = e.pointerId;
      const startY = e.clientY;
      let lastY = startY;
      let lastT = e.timeStamp;
      let velocity = 0;
      handle.setPointerCapture?.(pointerId);
      sheet.style.transition = 'none';
      sheet.style.animation = 'none';

      const move = (ev: PointerEvent) => {
        if (ev.pointerId !== pointerId) return;
        const dy = ev.clientY - startY;
        const dt = ev.timeStamp - lastT;
        if (dt > 0) velocity = (ev.clientY - lastY) / dt;
        lastY = ev.clientY;
        lastT = ev.timeStamp;
        // Pulling up past the rest position resists instead of following.
        const offset = dy > 0 ? dy : dy / 6;
        sheet.style.transform = `translateY(${offset}px)`;
      };

      const end = (ev: PointerEvent) => {
        if (ev.pointerId !== pointerId) return;
        handle.removeEventListener('pointermove', move);
        handle.removeEventListener('pointerup', end);
        handle.removeEventListener('pointercancel', end);
        const dy = ev.clientY - startY;
        const dismiss =
          ev.type === 'pointerup' &&
          (dy > DISMISS_DISTANCE || (dy > 16 && velocity > DISMISS_VELOCITY));
        sheet.style.transition = `transform ${EXIT_MS}ms var(--easing-drawer, ease-out)`;
        if (dismiss) {
          sheet.style.transform = 'translateY(100%)';
          window.setTimeout(() => onDismissRef.current(), EXIT_MS);
        } else {
          sheet.style.transform = '';
          window.setTimeout(() => {
            sheet.style.transition = '';
          }, EXIT_MS);
        }
      };

      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', end);
      handle.addEventListener('pointercancel', end);
    },
    [sheetRef],
  );

  return { onPointerDown };
}
