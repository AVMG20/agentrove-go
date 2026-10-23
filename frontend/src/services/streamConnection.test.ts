// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  client: {
    getValidToken: () => Promise.resolve('token'),
    getBaseUrl: () => 'http://api.test',
  },
  activeStreams: new Map<string, { chatId: string; isActive: boolean }>(),
}));

vi.mock('@/lib/api', () => ({ apiClient: mocks.client }));
vi.mock('@/utils/storage', () => ({ chatStorage: { getEventId: () => null } }));
vi.mock('@/store/streamStore', () => ({
  useStreamStore: {
    subscribe: () => () => {},
    getState: () => ({ activeStreams: mocks.activeStreams }),
  },
}));

class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static instances: FakeEventSource[] = [];

  readyState = FakeEventSource.CONNECTING;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, () => void>();

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, listener: () => void) {
    this.listeners.set(type, listener);
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
  open() {
    this.readyState = FakeEventSource.OPEN;
    this.onopen?.();
  }
  ping() {
    this.listeners.get('ping')?.();
  }
  fail() {
    this.onerror?.();
  }
}

let online = true;
let release: () => void = () => {};
let visibility: DocumentVisibilityState = 'visible';

async function setup() {
  vi.resetModules();
  const { streamConnection } = await import('@/services/streamConnection');
  const onConnectionFailure = vi.fn();
  streamConnection.configure({
    onOpen: () => {},
    onEnvelopeData: () => {},
    onConnectionFailure,
  });
  mocks.activeStreams.set('s1', { chatId: 'chat-1', isActive: true });
  release = streamConnection.retain(mocks.client as never);
  await vi.advanceTimersByTimeAsync(0);
  return { onConnectionFailure };
}

function latestSource(): FakeEventSource {
  return FakeEventSource.instances[FakeEventSource.instances.length - 1];
}

function resumePage() {
  visibility = 'hidden';
  document.dispatchEvent(new Event('visibilitychange'));
  visibility = 'visible';
  document.dispatchEvent(new Event('visibilitychange'));
}

describe('streamConnection page resume', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeEventSource);
    FakeEventSource.instances = [];
    mocks.activeStreams.clear();
    online = true;
    visibility = 'visible';
    vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online);
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibility);
  });

  afterEach(() => {
    // Tear down so this module instance's resume listener has nothing left to reopen.
    mocks.activeStreams.clear();
    release();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it('reopens a silent feed on resume but leaves a fresh one alone', async () => {
    await setup();
    latestSource().open();

    vi.advanceTimersByTime(5_000);
    resumePage();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeEventSource.instances).toHaveLength(1);

    // Half-open after sleep: OPEN but no pings, below the stall watchdog's threshold.
    vi.advanceTimersByTime(20_000);
    resumePage();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(FakeEventSource.instances[0].readyState).toBe(FakeEventSource.CLOSED);
  });

  it('does not burn retry budget or fail streams while offline', async () => {
    const { onConnectionFailure } = await setup();

    online = false;
    window.dispatchEvent(new Event('offline'));
    latestSource().fail();
    await vi.advanceTimersByTimeAsync(10 * 60_000);

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(onConnectionFailure).not.toHaveBeenCalled();

    online = true;
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeEventSource.instances).toHaveLength(2);
  });

  it('resets the retry budget on resume after failed retries', async () => {
    const { onConnectionFailure } = await setup();

    // Four failed attempts (budget is five) while online.
    for (let i = 0; i < 4; i += 1) {
      latestSource().fail();
      await vi.advanceTimersByTimeAsync(15_000);
    }
    resumePage();
    await vi.advanceTimersByTimeAsync(0);

    // Five more failures fit the fresh budget; the sixth exhausts it.
    for (let i = 0; i < 5; i += 1) {
      latestSource().fail();
      await vi.advanceTimersByTimeAsync(15_000);
    }
    expect(onConnectionFailure).not.toHaveBeenCalled();
    latestSource().fail();
    expect(onConnectionFailure).toHaveBeenCalledWith(['chat-1']);
  });
});
