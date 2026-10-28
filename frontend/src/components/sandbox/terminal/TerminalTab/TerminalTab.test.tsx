// @vitest-environment happy-dom

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TerminalTab } from './TerminalTab';

const mocks = vi.hoisted(() => {
  const writeCallbacks: Array<() => void> = [];
  const terminal = {
    cols: 120,
    rows: 40,
    attachCustomKeyEventHandler: vi.fn(),
    focus: vi.fn(),
    refresh: vi.fn(),
    reset: vi.fn(),
    write: vi.fn((_data: string, callback?: () => void) => {
      if (callback) {
        writeCallbacks.push(callback);
      }
    }),
  };

  return {
    fitTerminal: vi.fn(() => ({ rows: terminal.rows, cols: terminal.cols })),
    getValidToken: vi.fn().mockResolvedValue('terminal-token'),
    terminal,
    terminalRef: { current: terminal },
    wrapperRef: { current: null },
    writeCallbacks,
  };
});

vi.mock('@/hooks/useResolvedTheme', () => ({
  useResolvedTheme: () => 'dark',
}));

vi.mock('@/store/uiStore', () => ({
  useUIStore: (selector: (state: { theme: 'dark' }) => unknown) => selector({ theme: 'dark' }),
}));

vi.mock('@/hooks/useXterm', () => ({
  useXterm: () => ({
    fitTerminal: mocks.fitTerminal,
    isReady: true,
    searchAddonRef: { current: null },
    terminalRef: mocks.terminalRef,
    wrapperRef: mocks.wrapperRef,
  }),
}));

vi.mock('@/lib/api', () => ({
  apiClient: { getValidToken: mocks.getValidToken },
  WS_BASE_URL: 'wss://sandbox.example.test',
}));

vi.mock('@/utils/logger', () => ({
  logger: { error: vi.fn() },
}));

class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  static OPEN = 1;

  readyState = 0;
  send = vi.fn();
  private listeners = new Map<string, Set<EventListenerOrEventListenerObject>>();

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    this.listeners.get(type)?.delete(listener);
  }

  close(): void {
    this.readyState = 3;
  }

  open(): void {
    this.readyState = FakeWebSocket.OPEN;
    this.dispatch('open', new Event('open'));
  }

  message(data: Record<string, unknown>): void {
    this.dispatch('message', new MessageEvent('message', { data: JSON.stringify(data) }));
  }

  // Browsers fire `error` then `close` (1006) when the server goes away.
  drop(code = 1006, reason = ''): void {
    this.readyState = 3;
    this.dispatch('error', new Event('error'));
    this.dispatch('close', new CloseEvent('close', { code, reason }));
  }

  sentMessages(): unknown[] {
    return this.send.mock.calls
      .filter(([payload]) => typeof payload === 'string')
      .map(([payload]) => JSON.parse(payload as string) as unknown);
  }

  private dispatch(type: string, event: Event): void {
    for (const listener of this.listeners.get(type) ?? []) {
      if (typeof listener === 'function') {
        listener(event);
      } else {
        listener.handleEvent(event);
      }
    }
  }
}

describe('TerminalTab restored output', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.writeCallbacks.length = 0;
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    // The real xterm is opened asynchronously, so the mount-time focus frame
    // runs before terminalRef is populated. Keep that frame pending here too.
    vi.stubGlobal(
      'requestAnimationFrame',
      vi.fn(() => 1),
    );
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
  });

  it('requests a repaint after init, then focuses only after the first write is parsed', async () => {
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);

    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
    const websocket = FakeWebSocket.instances[0];

    act(() => websocket.open());
    expect(mocks.terminal.focus).not.toHaveBeenCalled();
    // No repaint request until the server acks init — the session may not
    // even have a PTY yet.
    expect(websocket.sentMessages()).not.toContainEqual({ type: 'refresh' });

    act(() => websocket.message({ type: 'init', id: 'pty-1', rows: 40, cols: 120 }));
    expect(websocket.sentMessages()).toContainEqual({ type: 'refresh' });

    act(() => websocket.message({ type: 'stdout', data: '\u001b[?1004hrestored screen' }));
    expect(mocks.terminal.write).toHaveBeenCalledWith(
      '\u001b[?1004hrestored screen',
      expect.any(Function),
    );
    expect(mocks.terminal.refresh).not.toHaveBeenCalled();
    expect(mocks.terminal.focus).not.toHaveBeenCalled();

    act(() => mocks.writeCallbacks[0]());
    expect(mocks.terminal.refresh).toHaveBeenCalledWith(0, 39);
    expect(mocks.terminal.focus).toHaveBeenCalledOnce();

    act(() => websocket.message({ type: 'stdout', data: 'later output' }));
    expect(mocks.terminal.write).toHaveBeenLastCalledWith('later output', undefined);
    expect(mocks.terminal.focus).toHaveBeenCalledOnce();
  });
});

describe('TerminalTab disconnects', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    vi.stubGlobal(
      'requestAnimationFrame',
      vi.fn(() => 1),
    );
    vi.stubGlobal('cancelAnimationFrame', vi.fn());
    vi.useFakeTimers();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  // Let the token mint promise resolve so the socket gets created.
  const flush = () => act(async () => {});

  it('auto-reconnects with backoff after an error+close, then offers Reconnect', async () => {
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(1);

    act(() => FakeWebSocket.instances[0].drop());
    expect(screen.getByText('Connection lost — reconnecting…')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Reconnect' })).toBeNull();

    for (const [index, delay] of [1000, 2000, 4000].entries()) {
      await act(() => vi.advanceTimersByTimeAsync(delay));
      expect(FakeWebSocket.instances).toHaveLength(index + 2);
      act(() => FakeWebSocket.instances[index + 1].drop());
    }

    expect(screen.getByText('Terminal disconnected')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(5);
  });

  it('resets the retry budget once the session is healthy again', async () => {
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);
    await flush();

    act(() => FakeWebSocket.instances[0].drop());
    await act(() => vi.advanceTimersByTimeAsync(1000));
    const second = FakeWebSocket.instances[1];
    act(() => second.open());
    act(() => second.message({ type: 'init', rows: 40, cols: 120 }));
    act(() => second.drop());

    // Back to the first (1s) backoff step rather than 2s.
    await act(() => vi.advanceTimersByTimeAsync(1000));
    expect(FakeWebSocket.instances).toHaveLength(3);
  });

  // The server closes a viewer with 1000 when another tab attaches to the same
  // terminal (or the shell exits); auto-reconnecting would steal it back forever.
  it('does not auto-reconnect after a normal closure', async () => {
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);
    await flush();
    const ws = FakeWebSocket.instances[0];
    act(() => ws.open());
    act(() => ws.message({ type: 'init', rows: 40, cols: 120 }));

    act(() => ws.drop(1000));
    await act(() => vi.advanceTimersByTimeAsync(10_000));
    expect(FakeWebSocket.instances).toHaveLength(1);
    expect(screen.getByText('Terminal disconnected')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Reconnect' })).toBeTruthy();
  });

  it('shows the server reason and a Reconnect button on a fatal close code', async () => {
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);
    await flush();

    act(() => FakeWebSocket.instances[0].drop(4004, 'Sandbox not found'));
    expect(screen.getByText('Sandbox not found')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Reconnect' })).toBeTruthy();
  });

  it('offers Reconnect when the auth token cannot be minted', async () => {
    mocks.getValidToken.mockRejectedValueOnce(new Error('offline'));
    render(<TerminalTab isVisible sandboxId="sandbox-1" terminalId="terminal-1" />);
    await flush();

    expect(screen.getByText('Terminal authentication failed')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    await flush();
    expect(FakeWebSocket.instances).toHaveLength(1);
  });
});
