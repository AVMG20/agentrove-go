// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { BrowserTab } from './BrowserTab';

const mocks = vi.hoisted(() => ({
  getValidToken: vi.fn().mockResolvedValue('browser-token'),
}));

vi.mock('@/lib/api', () => ({
  apiClient: { getValidToken: mocks.getValidToken },
  API_WS_URL: 'wss://app.example.test/api/v1',
}));

vi.mock('@/utils/logger', () => ({
  logger: { error: vi.fn() },
}));

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static OPEN = 1;

  readyState = 0;
  binaryType = 'blob';
  send = vi.fn();
  private listeners = new Map<string, Set<EventListener>>();

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: EventListener): void {
    const set = this.listeners.get(type) ?? new Set();
    set.add(listener);
    this.listeners.set(type, set);
  }

  removeEventListener(type: string, listener: EventListener): void {
    this.listeners.get(type)?.delete(listener);
  }

  close = vi.fn(() => {
    this.readyState = 3;
  });

  open(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.dispatch('open', new Event('open'));
  }

  message(data: Record<string, unknown> | ArrayBuffer): void {
    const payload = data instanceof ArrayBuffer ? data : JSON.stringify(data);
    this.dispatch('message', new MessageEvent('message', { data: payload }));
  }

  drop(code = 1006, reason = ''): void {
    this.readyState = 3;
    this.dispatch('close', new CloseEvent('close', { code, reason }));
  }

  sent(): Record<string, unknown>[] {
    return this.send.mock.calls.map(([p]) => JSON.parse(p as string) as Record<string, unknown>);
  }

  private dispatch(type: string, event: Event): void {
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }
}

function frame(seq: number, width = 1280, height = 800): ArrayBuffer {
  const head = new TextEncoder().encode(JSON.stringify({ seq, width, height }));
  const buf = new ArrayBuffer(4 + head.length + 2);
  new DataView(buf).setUint32(0, head.length);
  new Uint8Array(buf, 4).set(head);
  new Uint8Array(buf, 4 + head.length).set([0xff, 0xd8]);
  return buf;
}

const TABS = {
  type: 'tabs',
  active: 'T1',
  tabs: [
    { id: 'T1', title: 'My App', url: 'http://localhost:3000/' },
    { id: 'T2', title: '', url: 'about:blank' },
  ],
};

describe('BrowserTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    vi.useFakeTimers();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  const flush = () => act(async () => {});

  async function connect() {
    render(<BrowserTab isVisible sandboxId="box-1" />);
    await flush();
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1];
    act(() => ws.open());
    return ws;
  }

  it('authenticates first, then shows the browser once ready', async () => {
    render(<BrowserTab isVisible sandboxId="box-1" />);
    expect(screen.getByText('Connecting to the browser…')).toBeTruthy();
    await flush();
    const ws = FakeWebSocket.instances[0];
    expect(ws.url).toBe('wss://app.example.test/api/v1/sandbox/box-1/browser/ws');
    expect(ws.binaryType).toBe('arraybuffer');

    act(() => ws.open());
    expect(ws.sent()[0]).toEqual({ type: 'auth', token: 'browser-token' });

    act(() => ws.message({ type: 'status', state: 'starting' }));
    expect(screen.getByText('Starting the browser…')).toBeTruthy();

    act(() => ws.message({ type: 'status', state: 'ready' }));
    act(() => ws.message(TABS));
    expect(screen.queryByText('Starting the browser…')).toBeNull();
    expect(screen.getByRole('tab', { name: /My App/ }).getAttribute('aria-selected')).toBe('true');
    expect(screen.getByRole('tab', { name: /New tab/ })).toBeTruthy();
    expect((screen.getByLabelText('Address') as HTMLInputElement).value).toBe(
      'http://localhost:3000/',
    );
  });

  it('acks frames and sends commands', async () => {
    const ws = await connect();
    act(() => ws.message({ type: 'status', state: 'ready' }));
    act(() => ws.message(TABS));

    act(() => ws.message(frame(1)));
    expect(ws.sent()).toContainEqual({ type: 'ack', seq: 1 });

    fireEvent.click(screen.getByRole('tab', { name: /New tab/ }));
    expect(ws.sent()).toContainEqual({ type: 'tab.switch', id: 'T2' });
    fireEvent.click(screen.getByRole('button', { name: 'Close My App' }));
    expect(ws.sent()).toContainEqual({ type: 'tab.close', id: 'T1' });

    const address = screen.getByLabelText('Address');
    fireEvent.change(address, { target: { value: 'localhost:5173' } });
    fireEvent.keyDown(address, { key: 'Enter' });
    expect(ws.sent()).toContainEqual({ type: 'navigate', url: 'localhost:5173' });

    act(() => ws.message({ type: 'nav', canGoBack: true, canGoForward: false }));
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(ws.sent()).toContainEqual({ type: 'back' });
    expect((screen.getByRole('button', { name: 'Forward' }) as HTMLButtonElement).disabled).toBe(
      true,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Reset browser' }));
    fireEvent.click(screen.getByRole('button', { name: 'Reset' }));
    expect(ws.sent()).toContainEqual({ type: 'reset' });
  });

  it('forwards keys typed into the page but leaves paste to the paste event', async () => {
    const ws = await connect();
    act(() => ws.message({ type: 'status', state: 'ready' }));
    act(() => ws.message(TABS));
    const stage = screen.getByLabelText('Browser page');

    fireEvent.keyDown(stage, { key: 'a', code: 'KeyA', keyCode: 65, shiftKey: true });
    expect(ws.sent()).toContainEqual(
      expect.objectContaining({ type: 'key', event: 'down', key: 'a', keyCode: 65, modifiers: 8 }),
    );
    const before = ws.sent().length;
    fireEvent.keyDown(stage, { key: 'v', code: 'KeyV', ctrlKey: true });
    expect(ws.sent()).toHaveLength(before);
  });

  it('shows page dialogs and answers them', async () => {
    const ws = await connect();
    act(() => ws.message({ type: 'status', state: 'ready' }));
    act(() => ws.message(TABS));
    act(() => ws.message({ type: 'dialog', open: true, dialogType: 'confirm', message: 'Sure?' }));
    expect(screen.getByText('Sure?')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(ws.sent()).toContainEqual(expect.objectContaining({ type: 'dialog', accept: false }));
    act(() => ws.message({ type: 'dialog', open: false }));
    expect(screen.queryByText('Sure?')).toBeNull();
  });

  it('flags page changes the user did not make', async () => {
    const ws = await connect();
    act(() => ws.message({ type: 'status', state: 'ready' }));
    act(() => ws.message(TABS));
    await act(() => vi.advanceTimersByTimeAsync(5000));
    act(() =>
      ws.message({
        ...TABS,
        tabs: [{ id: 'T1', title: 'Login', url: 'http://localhost:3000/login' }],
      }),
    );
    expect(screen.getByText('Agent')).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(5000));
    expect(screen.queryByText('Agent')).toBeNull();
  });

  it('reconnects with backoff after a drop, then offers Reconnect', async () => {
    const ws = await connect();
    act(() => ws.drop());
    expect(screen.getByText('Connection lost — reconnecting…')).toBeTruthy();
    for (const [i, delay] of [1000, 2000, 4000].entries()) {
      await act(() => vi.advanceTimersByTimeAsync(delay));
      expect(FakeWebSocket.instances).toHaveLength(i + 2);
      act(() => FakeWebSocket.instances[i + 1].drop());
    }
    expect(screen.getByText('Browser disconnected')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(5);
  });

  it('shows the server reason without retrying when the box has no browser', async () => {
    const ws = await connect();
    act(() => ws.drop(4006, 'Browser unavailable: rebuild the sandbox image'));
    expect(screen.getByText('Browser unavailable: rebuild the sandbox image')).toBeTruthy();
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Reconnect' })).toBeTruthy();
  });

  it('only holds a connection while visible', async () => {
    const { rerender } = render(<BrowserTab isVisible={false} sandboxId="box-1" />);
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(0);

    rerender(<BrowserTab isVisible sandboxId="box-1" />);
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(1);

    rerender(<BrowserTab isVisible={false} sandboxId="box-1" />);
    expect(FakeWebSocket.instances[0].close).toHaveBeenCalled();
  });

  it('lets go of the browser when the page stays in the background', async () => {
    let state: DocumentVisibilityState = 'visible';
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => state);
    await connect();
    const ws = FakeWebSocket.instances[0];

    state = 'hidden';
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(ws.close).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(15_000));
    expect(ws.close).toHaveBeenCalled();

    state = 'visible';
    act(() => document.dispatchEvent(new Event('visibilitychange')));
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(2);
  });
});
