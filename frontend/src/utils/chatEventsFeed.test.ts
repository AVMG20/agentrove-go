// @vitest-environment happy-dom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { subscribeChatEventsFeed } from '@/utils/chatEventsFeed';

class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  addEventListener() {}
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.();
  }
}

let visibility: DocumentVisibilityState = 'visible';

function resumeAfter(ms: number) {
  visibility = 'hidden';
  document.dispatchEvent(new Event('visibilitychange'));
  vi.advanceTimersByTime(ms);
  visibility = 'visible';
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('subscribeChatEventsFeed page resume', () => {
  let sources: FakeEventSource[];
  let unsubscribe: () => void;
  const onResync = vi.fn();

  beforeEach(async () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
    visibility = 'visible';
    sources = [];
    onResync.mockReset();
    unsubscribe = subscribeChatEventsFeed({
      createSource: async () => {
        const source = new FakeEventSource();
        sources.push(source);
        return source as unknown as EventSource;
      },
      onChatEvent: () => {},
      onResync,
      logContext: 'test',
    });
    await vi.advanceTimersByTimeAsync(0);
    sources[0].open();
  });

  afterEach(() => {
    unsubscribe();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('keeps an open feed across a short tab switch', async () => {
    resumeAfter(5_000);
    await vi.advanceTimersByTimeAsync(0);
    expect(sources).toHaveLength(1);
    expect(onResync).not.toHaveBeenCalled();
  });

  it('replaces the feed after a long away and resyncs once reopened', async () => {
    resumeAfter(60_000);
    // Second resume while the reopen is in flight must not open another socket.
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(0);

    expect(sources).toHaveLength(2);
    expect(sources[0].readyState).toBe(FakeEventSource.CLOSED);
    sources[1].open();
    expect(onResync).toHaveBeenCalledTimes(1);
  });
});
