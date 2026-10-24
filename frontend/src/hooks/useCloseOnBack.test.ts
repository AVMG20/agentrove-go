// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import { useCloseOnBack } from './useCloseOnBack';

// jsdom's history.back() is async and fires popstate; simulate it synchronously
// over a small in-memory stack so the tests can follow each step.
let stack: unknown[];
beforeEach(() => {
  stack = [{ usr: null, key: 'k1', idx: 3 }];
  vi.spyOn(window.history, 'state', 'get').mockImplementation(() => stack[stack.length - 1]);
  vi.spyOn(window.history, 'pushState').mockImplementation((state) => {
    stack.push(state);
  });
  vi.spyOn(window.history, 'back').mockImplementation(() => {
    stack.pop();
    window.dispatchEvent(new PopStateEvent('popstate'));
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useCloseOnBack', () => {
  it('adds an entry carrying the router state, and back closes', () => {
    const onClose = vi.fn();
    renderHook(() => useCloseOnBack(true, onClose));

    expect(stack).toHaveLength(2);
    expect(stack[1]).toMatchObject({ key: 'k1', idx: 3 });
    act(() => window.history.back());
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('requestClose pops the entry and closes once', () => {
    const onClose = vi.fn();
    const { result } = renderHook(() => useCloseOnBack(true, onClose));

    act(() => result.current());
    expect(stack).toHaveLength(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('does not stack entries when the effect runs twice', () => {
    const { rerender } = renderHook(({ open }) => useCloseOnBack(open, vi.fn()), {
      initialProps: { open: true },
    });
    rerender({ open: true });
    expect(stack).toHaveLength(2);
  });

  it('closes directly when something navigated on top of its entry', () => {
    const onClose = vi.fn();
    const { result } = renderHook(() => useCloseOnBack(true, onClose));
    stack.push({ idx: 9 });

    act(() => result.current());
    expect(window.history.back).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
