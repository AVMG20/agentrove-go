import { useEffect } from 'react';
import { useUIStore } from '@/store/uiStore';
import { isIOS, isStandalone } from '@/utils/device';

// Opening starts only in a strip along the left edge of the page, the way iOS
// apps do it: a horizontal drag anywhere else belongs to the content (code
// blocks, tables, tab strips, text selection).
export const EDGE_ZONE_PX = 24;
// iOS browsers (not the home-screen app) own the first stretch of the edge for
// their back gesture, so the strip starts just past it there.
export const IOS_BACK_GESTURE_PX = 20;
// Movement before a touch commits to an axis, and how much more horizontal than
// vertical it must be to count as a drawer drag rather than a scroll.
const AXIS_LOCK_PX = 10;
const HORIZONTAL_BIAS = 1.5;
// Release past this share of the drawer, or flicked faster than this (and far
// enough that it wasn't a wobble), settles it the way the finger was going.
const SETTLE_PROGRESS = 0.4;
const FLICK_PX_PER_MS = 0.5;
const FLICK_MIN_PX = 30;
// Time to settle the whole width; a nearly-there drawer settles faster.
const SETTLE_MS = 280;
const SETTLE_MIN_MS = 120;

// Content that owns touch even at the edge: fields (text selection), the
// terminal and editor canvases.
const OWNS_TOUCH = 'input, textarea, canvas, .xterm, .monaco-editor, [contenteditable="true"]';
// While a dialog, menu or picker sheet is up, the finger is on it or its scrim:
// leave the drawer alone.
const OVERLAY_OPEN = '[aria-modal="true"], [role="listbox"], [role="menu"]';
// Closing drags start on the open drawer or on the scrim beside it.
const CLOSE_SURFACE = '#app-sidebar, [data-drawer-scrim]';

// A scroller under the finger that can still move the way the finger drags
// keeps the gesture: dragging right scrolls back towards its start.
function inScrollerMovable(element: Element | null, draggingRight: boolean): boolean {
  for (let el = element; el && el !== document.body; el = el.parentElement) {
    if (el.scrollWidth <= el.clientWidth) continue;
    const { overflowX } = window.getComputedStyle(el);
    if (overflowX !== 'auto' && overflowX !== 'scroll') continue;
    const canMove = draggingRight
      ? el.scrollLeft > 0
      : el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
    if (canMove) return true;
  }
  return false;
}

// Where the opening strip lies, in client px: past the landscape notch inset
// (the page column starts there) and, in an iOS browser, past the back gesture.
function edgeZone(): [number, number] {
  const page = document.getElementById('main-content');
  const inset = Math.max(0, page?.getBoundingClientRect().left ?? 0);
  const start = isIOS() && !isStandalone() ? Math.max(inset, IOS_BACK_GESTURE_PX) : 0;
  return [start, Math.max(inset, start) + EDGE_ZONE_PX];
}

// The phone drawer follows the finger: pull it open from the left edge, push it
// closed by dragging the open drawer (or the scrim) left; release to settle by
// distance or flick. While dragging, html[data-drawer-dragging] and
// --drawer-progress (0 closed … 1 open) drive the drawer and scrim (AppSidebar
// / Layout styles). Taps on the scrim and the title bar toggle cover the rest.
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
      if (!target || target.closest(OWNS_TOUCH)) return;
      if (document.querySelector(OVERLAY_OPEN)) return;
      const touch = e.touches[0];
      const opening = !useUIStore.getState().sidebarOpen;
      if (opening) {
        const [from, to] = edgeZone();
        if (touch.clientX < from || touch.clientX > to) return;
      } else if (!target.closest(CLOSE_SURFACE)) {
        return;
      }
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
        // Only the direction that moves the drawer counts, and a scroller that
        // can still move that way under the finger keeps the drag.
        const towardsTarget = start.opening ? dx > 0 : dx < 0;
        if (
          !horizontal ||
          !towardsTarget ||
          inScrollerMovable(e.target as Element | null, dx > 0)
        ) {
          axis = 'y';
          start = null;
          return;
        }
        axis = 'x';
        clearTimeout(settleTimer);
        root.style.removeProperty('--sidebar-transition-duration');
        root.setAttribute('data-drawer-dragging', '');
      }
      // The drawer has the finger now: the page under it mustn't scroll too.
      if (e.cancelable) e.preventDefault();
      const travelled = Math.min(1, Math.max(0, Math.abs(dx) / start.width));
      progress = start.opening ? travelled : 1 - travelled;
      root.style.setProperty('--drawer-progress', String(progress));
    };

    const onEnd = (e: TouchEvent) => {
      if (!start || axis !== 'x') {
        start = null;
        axis = null;
        return;
      }
      const touch = e.changedTouches[0];
      const dx = touch.clientX - start.x;
      const velocity = dx / Math.max(1, e.timeStamp - start.t);
      const flicked = Math.abs(dx) >= FLICK_MIN_PX;
      const open = start.opening
        ? progress > SETTLE_PROGRESS || (flicked && velocity > FLICK_PX_PER_MS)
        : !(progress < 1 - SETTLE_PROGRESS || (flicked && velocity < -FLICK_PX_PER_MS));
      start = null;
      axis = null;
      // Animate from where the finger left it over the distance still to go,
      // whether or not the state changes.
      // Set after the state change, so this replaces the shell's own (longer)
      // open/close timing.
      const remaining = open ? 1 - progress : progress;
      const duration = Math.round(Math.max(SETTLE_MIN_MS, SETTLE_MS * remaining));
      useUIStore.getState().setSidebarOpen(open);
      root.style.setProperty('--sidebar-transition-duration', `${duration}ms`);
      endDrag();
      clearTimeout(settleTimer);
      settleTimer = setTimeout(
        () => root.style.removeProperty('--sidebar-transition-duration'),
        duration + 50,
      );
    };

    const onCancel = () => {
      const wasDragging = axis === 'x';
      start = null;
      axis = null;
      if (!wasDragging) return;
      // Settle back to where it was rather than snapping.
      root.style.setProperty('--sidebar-transition-duration', `${SETTLE_MIN_MS}ms`);
      endDrag();
      clearTimeout(settleTimer);
      settleTimer = setTimeout(
        () => root.style.removeProperty('--sidebar-transition-duration'),
        SETTLE_MIN_MS + 50,
      );
    };

    document.addEventListener('touchstart', onStart, { passive: true });
    // Not passive: once the drawer owns a drag it stops the page scrolling.
    // Until then the handler returns straight away.
    document.addEventListener('touchmove', onMove, { passive: false });
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
