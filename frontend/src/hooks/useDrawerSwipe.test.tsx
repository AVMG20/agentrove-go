// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, renderHook } from '@testing-library/react';
import { useUIStore } from '@/store/uiStore';
import { useDrawerSwipe } from './useDrawerSwipe';

const root = document.documentElement;
let drawer: HTMLElement;
let scrim: HTMLElement;
let main: HTMLElement;

// jsdom has no Touch constructor: plain events carrying the touch fields.
function touch(type: string, target: Element, x: number, y: number, time: number) {
  const event = new Event(type, { bubbles: true, cancelable: true });
  const point = { clientX: x, clientY: y };
  Object.assign(event, {
    touches: type === 'touchend' ? [] : [point],
    changedTouches: [point],
  });
  Object.defineProperty(event, 'timeStamp', { value: time });
  target.dispatchEvent(event);
  return event;
}

// A finger moving from (x0, y) to (x1, y1) over `ms`, in 10 steps. Returns the
// move events, to check which ones the drawer claimed.
function swipe(
  target: Element,
  x0: number,
  x1: number,
  { y = 400, y1, ms = 300 }: { y?: number; y1?: number; ms?: number } = {},
) {
  y1 ??= y;
  const moves: Event[] = [];
  touch('touchstart', target, x0, y, 0);
  for (let i = 1; i <= 10; i++) {
    moves.push(
      touch('touchmove', target, x0 + ((x1 - x0) * i) / 10, y + ((y1 - y) * i) / 10, (ms * i) / 10),
    );
  }
  touch('touchend', target, x1, y1, ms);
  return moves;
}

const isOpen = () => useUIStore.getState().sidebarOpen;

beforeEach(() => {
  drawer = document.createElement('aside');
  drawer.id = 'app-sidebar';
  drawer.getBoundingClientRect = () => ({ width: 300 }) as DOMRect;
  scrim = document.createElement('div');
  scrim.setAttribute('data-drawer-scrim', '');
  main = document.createElement('main');
  main.id = 'main-content';
  main.getBoundingClientRect = () => ({ left: 0 }) as DOMRect;
  document.body.append(scrim, drawer, main);
  useUIStore.getState().setSidebarOpen(false);
});

afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

describe('useDrawerSwipe', () => {
  it('opens from the left edge, tracking the finger', () => {
    renderHook(() => useDrawerSwipe(true));
    touch('touchstart', main, 10, 400, 0);
    const move = touch('touchmove', main, 70, 400, 50);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(true);
    expect(Number(root.style.getPropertyValue('--drawer-progress'))).toBeCloseTo(0.2);
    // The drawer owns the drag: the page doesn't scroll under it.
    expect(move.defaultPrevented).toBe(true);
    touch('touchmove', main, 200, 400, 400);
    touch('touchend', main, 200, 400, 400);
    expect(isOpen()).toBe(true);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
  });

  it('ignores horizontal drags that start away from the edge', () => {
    renderHook(() => useDrawerSwipe(true));
    const moves = swipe(main, 60, 300, { ms: 120 });
    expect(isOpen()).toBe(false);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
    expect(moves.some((e) => e.defaultPrevented)).toBe(false);
  });

  it('springs back on a short slow drag, and opens on a short flick', () => {
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 10, 90, { ms: 600 });
    expect(isOpen()).toBe(false);
    swipe(main, 10, 90, { ms: 80 });
    expect(isOpen()).toBe(true);
  });

  it('does not count a tiny fast wobble as a flick', () => {
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 10, 32, { ms: 20 });
    expect(isOpen()).toBe(false);
  });

  it('closes the open drawer when it or the scrim is dragged left', () => {
    useUIStore.getState().setSidebarOpen(true);
    renderHook(() => useDrawerSwipe(true));
    swipe(drawer, 250, 40);
    expect(isOpen()).toBe(false);

    useUIStore.getState().setSidebarOpen(true);
    swipe(scrim, 350, 120);
    expect(isOpen()).toBe(false);
  });

  it('keeps the open drawer on a right drag or a drag elsewhere', () => {
    useUIStore.getState().setSidebarOpen(true);
    renderHook(() => useDrawerSwipe(true));
    swipe(drawer, 40, 250);
    expect(isOpen()).toBe(true);
    // Something outside the drawer and scrim (a toast, say).
    swipe(main, 350, 40);
    expect(isOpen()).toBe(true);
  });

  it('leaves vertical scrolls alone', () => {
    renderHook(() => useDrawerSwipe(true));
    const moves = swipe(main, 10, 40, { y: 500, y1: 200 });
    expect(isOpen()).toBe(false);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
    expect(moves.some((e) => e.defaultPrevented)).toBe(false);
  });

  it('leaves a horizontal scroller that can still scroll that way', () => {
    renderHook(() => useDrawerSwipe(true));
    const scroller = document.createElement('div');
    scroller.style.overflowX = 'auto';
    Object.defineProperties(scroller, {
      scrollWidth: { value: 800 },
      clientWidth: { value: 300 },
      scrollLeft: { value: 120, writable: true },
    });
    main.append(scroller);
    swipe(scroller, 10, 250);
    expect(isOpen()).toBe(false);

    // Scrolled to its start, a right drag has nowhere to go: the drawer takes it.
    scroller.scrollLeft = 0;
    swipe(scroller, 10, 250);
    expect(isOpen()).toBe(true);
  });

  it('starts past the back-gesture strip in an iOS browser, not in the home-screen app', () => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
      'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)',
    );
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 8, 250);
    expect(isOpen()).toBe(false);
    swipe(main, 30, 250);
    expect(isOpen()).toBe(true);

    useUIStore.getState().setSidebarOpen(false);
    Object.defineProperty(navigator, 'standalone', { configurable: true, value: true });
    try {
      swipe(main, 8, 250);
      expect(isOpen()).toBe(true);
    } finally {
      delete (navigator as { standalone?: boolean }).standalone;
    }
  });

  it('moves the strip past the notch inset in landscape', () => {
    main.getBoundingClientRect = () => ({ left: 44 }) as DOMRect;
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 60, 300);
    expect(isOpen()).toBe(true);
  });

  it('ignores fields and open dialogs, menus or pickers', () => {
    renderHook(() => useDrawerSwipe(true));
    const input = document.createElement('input');
    main.append(input);
    swipe(input, 10, 250);
    expect(isOpen()).toBe(false);

    for (const [name, value] of [
      ['aria-modal', 'true'],
      ['role', 'listbox'],
      ['role', 'menu'],
    ]) {
      const overlay = document.createElement('div');
      overlay.setAttribute(name, value);
      document.body.append(overlay);
      swipe(main, 10, 250);
      expect(isOpen()).toBe(false);
      overlay.remove();
    }
  });

  it('settles back when the browser cancels the touch', () => {
    renderHook(() => useDrawerSwipe(true));
    touch('touchstart', main, 10, 400, 0);
    touch('touchmove', main, 120, 400, 50);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(true);
    touch('touchcancel', main, 120, 400, 60);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
    expect(isOpen()).toBe(false);
  });

  it('does nothing when disabled', () => {
    renderHook(() => useDrawerSwipe(false));
    swipe(main, 10, 250);
    expect(isOpen()).toBe(false);
  });
});
