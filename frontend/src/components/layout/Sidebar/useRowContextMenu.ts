import { useEffect, useRef, type MouseEvent, type PointerEvent } from 'react';

// Hold this long without moving to open a row's menu by touch.
export const LONG_PRESS_MS = 450;
// A finger drifting further than this is scrolling, not pressing.
const MOVE_TOLERANCE = 10;

export interface RowContextMenuHandlers {
  onPointerDown: (e: PointerEvent<HTMLElement>) => void;
  onPointerMove: (e: PointerEvent<HTMLElement>) => void;
  onPointerUp: () => void;
  onPointerCancel: () => void;
  onContextMenu: (e: MouseEvent<HTMLElement>) => void;
  onClickCapture: (e: MouseEvent<HTMLElement>) => void;
}

// Opens a row's options the native way: right-click with a mouse, long-press
// with a finger. The long press fires on a timer (iOS sends no contextmenu
// event), cancels when the finger moves (the list is scrolling) and swallows
// the click that follows lifting the finger, so the row doesn't also open.
// `open` gets the viewport point to anchor a floating menu to.
export function useRowContextMenu(
  open: ((point: { x: number; y: number }) => void) | undefined,
): RowContextMenuHandlers | undefined {
  const timerRef = useRef<number | null>(null);
  const startRef = useRef<{ x: number; y: number } | null>(null);
  // Set when a long press opened the menu; eats the trailing click/contextmenu.
  const firedAtRef = useRef(0);

  const clear = () => {
    if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    timerRef.current = null;
    startRef.current = null;
  };

  useEffect(() => clear, []);

  if (!open) return undefined;

  const recentlyFired = () => Date.now() - firedAtRef.current < 1000;

  return {
    onPointerDown: (e) => {
      if (e.pointerType === 'mouse' || !e.isPrimary) return;
      // Presses on the row's own buttons keep their meaning.
      if ((e.target as HTMLElement).closest('[data-row-action]')) return;
      clear();
      const point = { x: e.clientX, y: e.clientY };
      startRef.current = point;
      timerRef.current = window.setTimeout(() => {
        timerRef.current = null;
        firedAtRef.current = Date.now();
        navigator.vibrate?.(8);
        open(point);
      }, LONG_PRESS_MS);
    },
    onPointerMove: (e) => {
      const start = startRef.current;
      if (!start) return;
      if (Math.hypot(e.clientX - start.x, e.clientY - start.y) > MOVE_TOLERANCE) clear();
    },
    onPointerUp: clear,
    onPointerCancel: clear,
    onContextMenu: (e) => {
      if ((e.target as HTMLElement).closest('input, textarea')) return;
      e.preventDefault();
      clear();
      // Android also raises contextmenu for the press the timer already handled.
      if (recentlyFired()) return;
      open({ x: e.clientX, y: e.clientY });
    },
    onClickCapture: (e) => {
      if (!recentlyFired()) return;
      firedAtRef.current = 0;
      e.preventDefault();
      e.stopPropagation();
    },
  };
}
