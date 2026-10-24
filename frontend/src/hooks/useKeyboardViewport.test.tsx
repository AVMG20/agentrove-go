// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, renderHook } from '@testing-library/react';
import { setViewport } from '@/test-utils/device';
import { useKeyboardViewport } from './useKeyboardViewport';

// iOS-style visual viewport: the layout viewport (innerHeight) stays, the
// visual one shrinks for the keyboard.
const vv = Object.assign(new EventTarget(), { height: 844, offsetTop: 0, scale: 1 });
const root = document.documentElement;

function setVisual(height: number, { offsetTop = 0, scale = 1 } = {}) {
  vv.height = height;
  vv.offsetTop = offsetTop;
  vv.scale = scale;
  vv.dispatchEvent(new Event('resize'));
}

beforeEach(() => {
  Object.defineProperty(window, 'visualViewport', { configurable: true, value: vv });
  // Run frames at once; id 0 so the hook's "frame pending" guard resets.
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
    cb(0);
    return 0;
  });
  setViewport(390, 844);
  vv.height = 844;
  vv.offsetTop = 0;
  vv.scale = 1;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const vars = () => ({
  open: root.hasAttribute('data-keyboard-open'),
  height: root.style.getPropertyValue('--app-height'),
  offset: root.style.getPropertyValue('--app-offset-top'),
  inset: root.style.getPropertyValue('--keyboard-inset'),
});

describe('useKeyboardViewport', () => {
  it('pins the shell to the visible area while the keyboard is up', () => {
    renderHook(() => useKeyboardViewport(true));
    expect(vars().open).toBe(false);

    setVisual(464, { offsetTop: 20 });
    expect(vars()).toEqual({ open: true, height: '464px', offset: '20px', inset: '360px' });

    setVisual(844);
    expect(vars()).toEqual({ open: false, height: '', offset: '', inset: '' });
  });

  it("doesn't take pinch zoom for a keyboard", () => {
    renderHook(() => useKeyboardViewport(true));
    setVisual(844 / 2, { scale: 2 });
    expect(vars().open).toBe(false);
  });

  it('follows a rotation with the keyboard up', () => {
    renderHook(() => useKeyboardViewport(true));
    setVisual(464);
    setViewport(844, 390);
    setVisual(190);
    expect(vars()).toMatchObject({ open: true, height: '190px', inset: '200px' });
  });

  it('cleans up on unmount and does nothing when disabled', () => {
    const { unmount } = renderHook(() => useKeyboardViewport(true));
    setVisual(464);
    unmount();
    expect(vars()).toEqual({ open: false, height: '', offset: '', inset: '' });

    renderHook(() => useKeyboardViewport(false));
    setVisual(464);
    expect(vars().open).toBe(false);
  });
});
