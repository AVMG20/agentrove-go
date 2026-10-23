// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { subscribePageResume } from '@/utils/pageResume';

let visibility: DocumentVisibilityState = 'visible';

function setVisibility(state: DocumentVisibilityState) {
  visibility = state;
  document.dispatchEvent(new Event('visibilitychange'));
}

function pageShow(persisted: boolean) {
  const event = new Event('pageshow') as PageTransitionEvent;
  Object.defineProperty(event, 'persisted', { value: persisted });
  window.dispatchEvent(event);
}

describe('subscribePageResume', () => {
  let unsubscribe: () => void = () => {};

  beforeEach(() => {
    vi.useFakeTimers();
    visibility = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
  });

  afterEach(() => {
    unsubscribe();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('reports how long the page was hidden when it becomes visible', () => {
    const callback = vi.fn();
    unsubscribe = subscribePageResume(callback);

    setVisibility('hidden');
    expect(callback).not.toHaveBeenCalled();
    vi.advanceTimersByTime(40_000);
    setVisibility('visible');

    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith({ idleMs: 40_000 });
  });

  it('measures offline gaps and keeps the earliest away mark', () => {
    const callback = vi.fn();
    unsubscribe = subscribePageResume(callback);

    setVisibility('hidden');
    vi.advanceTimersByTime(10_000);
    window.dispatchEvent(new Event('offline'));
    vi.advanceTimersByTime(5_000);
    // Online while still hidden: fires, but keeps the mark for the visible resume.
    window.dispatchEvent(new Event('online'));
    expect(callback).toHaveBeenLastCalledWith({ idleMs: 15_000 });

    vi.advanceTimersByTime(5_000);
    setVisibility('visible');
    expect(callback).toHaveBeenLastCalledWith({ idleMs: 20_000 });

    // Mark cleared once visible.
    window.dispatchEvent(new Event('online'));
    expect(callback).toHaveBeenLastCalledWith({ idleMs: 0 });
  });

  it('keeps the away mark while visible but still offline', () => {
    const callback = vi.fn();
    unsubscribe = subscribePageResume(callback);
    let online = true;
    vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online);

    setVisibility('hidden');
    online = false;
    window.dispatchEvent(new Event('offline'));
    vi.advanceTimersByTime(60_000);
    setVisibility('visible');
    expect(callback).toHaveBeenLastCalledWith({ idleMs: 60_000 });

    vi.advanceTimersByTime(5_000);
    online = true;
    window.dispatchEvent(new Event('online'));
    expect(callback).toHaveBeenLastCalledWith({ idleMs: 65_000 });
  });

  it('fires on bfcache restore only', () => {
    const callback = vi.fn();
    unsubscribe = subscribePageResume(callback);

    pageShow(false);
    expect(callback).not.toHaveBeenCalled();

    window.dispatchEvent(new Event('pagehide'));
    vi.advanceTimersByTime(3_000);
    pageShow(true);
    expect(callback).toHaveBeenCalledWith({ idleMs: 3_000 });
  });

  it('stops firing after unsubscribe', () => {
    const callback = vi.fn();
    subscribePageResume(callback)();

    setVisibility('hidden');
    setVisibility('visible');
    window.dispatchEvent(new Event('online'));

    expect(callback).not.toHaveBeenCalled();
  });
});
