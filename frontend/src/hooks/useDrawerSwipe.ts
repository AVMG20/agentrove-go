import { useEffect } from 'react';
import { useUIStore } from '@/store/uiStore';

// Movement before a touch commits to an axis, and how much more horizontal than
// vertical it must be to count as a swipe rather than a scroll.
const AXIS_LOCK_PX = 8;
const HORIZONTAL_BIAS = 1.2;
// Release past this share of the drawer, or flicked faster than this, settles it
// the way the finger was going.
const SETTLE_PROGRESS = 0.35;
const FLICK_PX_PER_MS = 0.35;
// Safari's own back gesture starts this close to the left edge.
const BROWSER_BACK_EDGE_PX = 24;
const SETTLE_MS = 220;

// Content that owns horizontal touch: fields (text selection), the terminal,
// editor and browser canvases, dialogs and sheets.
const OWNS_TOUCH =
  'input, textarea, canvas, .xterm, .monaco-editor, [role="dialog"], [role="listbox"]';

function inHorizontalScroller(element: Element | null): boolean {
  for (let el = element; el && el !== document.body; el = el.parentElement) {
    const { overflowX } = window.getComputedStyle(el);
    if ((overflowX === 'auto' || overflowX === 'scroll') && el.scrollWidth > el.clientWidth) {
      return true;
    }
  }
  return false;
}

const isStandalone = () =>
  window.matchMedia?.('(display-mode: standalone)').matches ||
  (navigator as Navigator & { standalone?: boolean }).standalone === true;

// The phone drawer follows the finger: drag right anywhere to pull it open,
// drag left to push it closed, release to settle (by distance or flick). While
// dragging, html[data-drawer-dragging] and --drawer-progress (0 closed … 1 open)
// drive the drawer and scrim (AppSidebar / Layout styles).
export function useDrawerSwipe(enabled: boolean) {
  useEffect(() => {
    if (!enabled) return;
    const root = document.documentElement;
    let start: { x: number; y: number; t: number; opening: boolean; width: number } | null = null;
    let axis: 'x' | 'y' | null = null;
    let progress = 0;
    let settleTimer: ReturnType<typeof setTimeout> | undefined;

    const endDrag = () => {
      root.removeAttribute('data-drawer-dragging');
      root.style.removeProperty('--drawer-progress');
    };

    const onStart = (e: TouchEvent) => {
      start = null;
      axis = null;
      if (e.touches.length !== 1) return;
      const target = e.target as Element | null;
      if (target?.closest(OWNS_TOUCH) || inHorizontalScroller(target)) return;
      const touch = e.touches[0];
      const opening = !useUIStore.getState().sidebarOpen;
      if (opening && touch.clientX < BROWSER_BACK_EDGE_PX && !isStandalone()) return;
      const drawer = document.getElementById('app-sidebar');
      const width = drawer?.getBoundingClientRect().width ?? 0;
      if (!width) return;
      start = { x: touch.clientX, y: touch.clientY, t: e.timeStamp, opening, width };
    };

    const onMove = (e: TouchEvent) => {
      if (!start) return;
      const touch = e.touches[0];
      const dx = touch.clientX - start.x;
      const dy = touch.clientY - start.y;
      if (axis === null) {
        if (Math.abs(dx) < AXIS_LOCK_PX && Math.abs(dy) < AXIS_LOCK_PX) return;
        const horizontal = Math.abs(dx) > Math.abs(dy) * HORIZONTAL_BIAS;
        // Only the direction that moves the drawer counts.
        const towardsTarget = start.opening ? dx > 0 : dx < 0;
        axis = horizontal && towardsTarget ? 'x' : 'y';
        if (axis === 'y') {
          start = null;
          return;
        }
        clearTimeout(settleTimer);
        root.setAttribute('data-drawer-dragging', '');
      }
      const travelled = Math.min(1, Math.max(0, Math.abs(dx) / start.width));
      progress = start.opening ? travelled : 1 - travelled;
      root.style.setProperty('--drawer-progress', String(progress));
    };

    const onEnd = (e: TouchEvent) => {
      if (!start || axis !== 'x') {
        start = null;
        return;
      }
      const touch = e.changedTouches[0];
      const velocity = (touch.clientX - start.x) / Math.max(1, e.timeStamp - start.t);
      const open = start.opening
        ? progress > SETTLE_PROGRESS || velocity > FLICK_PX_PER_MS
        : !(progress < 1 - SETTLE_PROGRESS || velocity < -FLICK_PX_PER_MS);
      start = null;
      axis = null;
      // Animate from where the finger left it, whether or not the state changes.
      root.style.setProperty('--sidebar-transition-duration', `${SETTLE_MS}ms`);
      useUIStore.getState().setSidebarOpen(open);
      endDrag();
      clearTimeout(settleTimer);
      settleTimer = setTimeout(
        () => root.style.removeProperty('--sidebar-transition-duration'),
        SETTLE_MS + 50,
      );
    };

    const onCancel = () => {
      start = null;
      axis = null;
      endDrag();
    };

    document.addEventListener('touchstart', onStart, { passive: true });
    document.addEventListener('touchmove', onMove, { passive: true });
    document.addEventListener('touchend', onEnd, { passive: true });
    document.addEventListener('touchcancel', onCancel, { passive: true });
    return () => {
      clearTimeout(settleTimer);
      endDrag();
      document.removeEventListener('touchstart', onStart);
      document.removeEventListener('touchmove', onMove);
      document.removeEventListener('touchend', onEnd);
      document.removeEventListener('touchcancel', onCancel);
    };
  }, [enabled]);
}
