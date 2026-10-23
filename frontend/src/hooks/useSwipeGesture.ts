import { useEffect, useRef, useCallback } from 'react';

interface SwipeGestureOptions {
  onSwipeRight?: () => void;
  onSwipeLeft?: () => void;
  swipeThreshold?: number;
  enabled?: boolean;
}

function isInHorizontallyScrollableElement(element: Element | null): boolean {
  while (element && element !== document.body) {
    const style = window.getComputedStyle(element);
    const overflowX = style.overflowX;

    if (overflowX === 'auto' || overflowX === 'scroll') {
      if (element.scrollWidth > element.clientWidth) {
        return true;
      }
    }

    element = element.parentElement;
  }
  return false;
}

export function useSwipeGesture({
  onSwipeRight,
  onSwipeLeft,
  swipeThreshold = 50,
  enabled = true,
}: SwipeGestureOptions) {
  const touchStartX = useRef<number | null>(null);
  const touchStartY = useRef<number | null>(null);
  const isInScrollable = useRef<boolean>(false);

  const handleTouchStart = useCallback(
    (e: TouchEvent) => {
      if (!enabled) return;

      const touch = e.touches[0];
      touchStartX.current = touch.clientX;
      touchStartY.current = touch.clientY;

      isInScrollable.current = isInHorizontallyScrollableElement(e.target as Element);
    },
    [enabled],
  );

  const handleTouchEnd = useCallback(
    (e: TouchEvent) => {
      if (!enabled || touchStartX.current === null || touchStartY.current === null) {
        return;
      }

      const touch = e.changedTouches[0];
      const deltaX = touch.clientX - touchStartX.current;
      const deltaY = touch.clientY - touchStartY.current;

      const wasInScrollable = isInScrollable.current;
      touchStartX.current = null;
      touchStartY.current = null;
      isInScrollable.current = false;

      if (wasInScrollable) {
        return;
      }

      if (Math.abs(deltaY) > Math.abs(deltaX)) {
        return;
      }

      if (deltaX > swipeThreshold && onSwipeRight) {
        onSwipeRight();
      } else if (deltaX < -swipeThreshold && onSwipeLeft) {
        onSwipeLeft();
      }
    },
    [enabled, swipeThreshold, onSwipeRight, onSwipeLeft],
  );

  useEffect(() => {
    if (!enabled) return;

    document.addEventListener('touchstart', handleTouchStart, { passive: true });
    document.addEventListener('touchend', handleTouchEnd, { passive: true });

    return () => {
      document.removeEventListener('touchstart', handleTouchStart);
      document.removeEventListener('touchend', handleTouchEnd);
    };
  }, [enabled, handleTouchStart, handleTouchEnd]);
}
