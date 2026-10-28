// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, renderHook } from '@testing-library/react';
import { useUIStore } from '@/store/uiStore';
import { useDrawerSwipe } from './useDrawerSwipe';

const root = document.documentElement;
let drawer: HTMLElement;
let main: HTMLElement;

// jsdom has no Touch constructor: plain events carrying the touch fields.
function touch(type: string, target: Element, x: number, y: number, time: number) {
  const event = new Event(type, { bubbles: true });
  const point = { clientX: x, clientY: y };
  Object.assign(event, {
    touches: type === 'touchend' ? [] : [point],
    changedTouches: [point],
  });
  Object.defineProperty(event, 'timeStamp', { value: time });
  target.dispatchEvent(event);
}

// A finger moving from (x0, y) to (x1, y) over `ms`, in 10 steps.
function swipe(
  target: Element,
  x0: number,
  x1: number,
  { y = 400, y1, ms = 300 }: { y?: number; y1?: number; ms?: number } = {},
) {
  y1 ??= y;
  touch('touchstart', target, x0, y, 0);
  for (let i = 1; i <= 10; i++) {
    touch('touchmove', target, x0 + ((x1 - x0) * i) / 10, y + ((y1 - y) * i) / 10, (ms * i) / 10);
  }
  touch('touchend', target, x1, y1, ms);
}

const isOpen = () => useUIStore.getState().sidebarOpen;

beforeEach(() => {
  drawer = document.createElement('aside');
  drawer.id = 'app-sidebar';
  drawer.getBoundingClientRect = () => ({ width: 300 }) as DOMRect;
  main = document.createElement('main');
  document.body.append(drawer, main);
  useUIStore.getState().setSidebarOpen(false);
});

afterEach(() => {
  cleanup();
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

describe('useDrawerSwipe', () => {
  it('opens the drawer when dragged past the threshold, tracking the finger', () => {
    renderHook(() => useDrawerSwipe(true));
    touch('touchstart', main, 60, 400, 0);
    touch('touchmove', main, 120, 400, 50);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(true);
    expect(Number(root.style.getPropertyValue('--drawer-progress'))).toBeCloseTo(0.2);
    touch('touchmove', main, 200, 400, 400);
    touch('touchend', main, 200, 400, 400);
    expect(isOpen()).toBe(true);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
  });

  it('springs back on a short slow drag, and opens on a short flick', () => {
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 60, 120, { ms: 600 });
    expect(isOpen()).toBe(false);
    swipe(main, 60, 120, { ms: 80 });
    expect(isOpen()).toBe(true);
  });

  it('closes the open drawer when dragged left', () => {
    useUIStore.getState().setSidebarOpen(true);
    renderHook(() => useDrawerSwipe(true));
    swipe(drawer, 250, 40);
    expect(isOpen()).toBe(false);
  });

  it('leaves vertical scrolls alone', () => {
    renderHook(() => useDrawerSwipe(true));
    swipe(main, 100, 130, { y: 500, y1: 200 });
    expect(isOpen()).toBe(false);
    expect(root.hasAttribute('data-drawer-dragging')).toBe(false);
  });

  it('ignores fields, the left edge (Safari back) and open dialogs or pickers', () => {
    renderHook(() => useDrawerSwipe(true));
    const input = document.createElement('input');
    main.append(input);
    swipe(input, 60, 250);
    expect(isOpen()).toBe(false);

    swipe(main, 10, 250);
    expect(isOpen()).toBe(false);

    const dialog = document.createElement('div');
    dialog.setAttribute('aria-modal', 'true');
    document.body.append(dialog);
    swipe(main, 60, 250);
    expect(isOpen()).toBe(false);
    dialog.remove();

    const sheet = document.createElement('div');
    sheet.setAttribute('role', 'listbox');
    document.body.append(sheet);
    swipe(main, 60, 250);
    expect(isOpen()).toBe(false);
  });

  it('does nothing when disabled', () => {
    renderHook(() => useDrawerSwipe(false));
    swipe(main, 60, 250);
    expect(isOpen()).toBe(false);
  });
});
