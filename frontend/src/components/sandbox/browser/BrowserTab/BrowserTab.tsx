import { useCallback, useEffect, useRef, useState } from 'react';
import type {
  ClipboardEvent as ReactClipboardEvent,
  CompositionEvent as ReactCompositionEvent,
  FormEvent as ReactFormEvent,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react';
import clsx from 'clsx';
import { ArrowLeft, ArrowRight, Bot, Keyboard, Plus, RotateCw, Trash2, X } from 'lucide-react';

import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { apiClient, API_WS_URL } from '@/lib/api';
import { logger } from '@/utils/logger';
import {
  cdpModifiers,
  fitRect,
  isLocalShortcut,
  mouseButtonName,
  parseFrame,
  parseServerMessage,
  tabLabel,
  toPagePoint,
  VIEWPORT_PRESETS,
  wheelDelta,
  type BrowserTabInfo,
  type FrameHeader,
  type Rect,
} from '@/components/sandbox/browser/browserProtocol';
import styles from './BrowserTab.module.scss';

export interface BrowserTabProps {
  // Connected only while visible: a hidden viewer costs the box nothing.
  isVisible: boolean;
  sandboxId?: string;
}

type ConnectionState =
  | 'idle'
  | 'connecting'
  | 'starting'
  | 'ready'
  | 'restarting'
  | 'reconnecting'
  | 'error'
  | 'disconnected';

interface DialogState {
  dialogType: string;
  message: string;
  defaultPrompt: string;
}

// Server close codes: auth failed, sandbox not found, browser unavailable,
// too many viewers. Retrying won't help with any of them.
const FATAL_CLOSE_CODES = new Set([4001, 4004, 4006, 4008]);
const WS_CLOSE_NORMAL = 1000;
const MAX_AUTO_RECONNECTS = 3;
const RECONNECT_BASE_DELAY_MS = 1000;
// A page change this long after the user's last input was the agent's.
const AGENT_QUIET_MS = 3000;
const AGENT_BADGE_MS = 4000;
const NOTICE_MS = 4000;
const SCREEN_REPORT_DELAY_MS = 300;
const DOUBLE_CLICK_MS = 500;
const DOUBLE_CLICK_SLOP_PX = 5;
// A page left in a background browser tab this long lets go of the box's
// browser (and the box), like a closed viewer.
const HIDDEN_DISCONNECT_MS = 15_000;

// useLongHidden is true once the document has been hidden for a while.
function useLongHidden(): boolean {
  const [hidden, setHidden] = useState(false);
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const update = () => {
      if (timer) clearTimeout(timer);
      timer = null;
      if (document.visibilityState === 'hidden') {
        timer = setTimeout(() => setHidden(true), HIDDEN_DISCONNECT_MS);
      } else {
        setHidden(false);
      }
    };
    update();
    document.addEventListener('visibilitychange', update);
    return () => {
      document.removeEventListener('visibilitychange', update);
      if (timer) clearTimeout(timer);
    };
  }, []);
  return hidden;
}

export function BrowserTab({ isVisible, sandboxId }: BrowserTabProps) {
  const [connection, setConnection] = useState<ConnectionState>('idle');
  const [closeReason, setCloseReason] = useState<string | null>(null);
  const [connectAttempt, setConnectAttempt] = useState(0);
  const [tabs, setTabs] = useState<BrowserTabInfo[]>([]);
  const [activeId, setActiveId] = useState('');
  const [nav, setNav] = useState({ canGoBack: false, canGoForward: false });
  const [loading, setLoading] = useState(false);
  const [dialog, setDialog] = useState<DialogState | null>(null);
  const [promptText, setPromptText] = useState('');
  const [notice, setNotice] = useState<string | null>(null);
  const [urlDraft, setUrlDraft] = useState<string | null>(null);
  const [agentActive, setAgentActive] = useState(false);
  const [pageSize, setPageSize] = useState<{ width: number; height: number } | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);
  const [resetting, setResetting] = useState(false);
  const longHidden = useLongHidden();
  const connected = isVisible && !longHidden;

  const wsRef = useRef<WebSocket | null>(null);
  const stageRef = useRef<HTMLDivElement | null>(null);
  // Keys go to a hidden textarea rather than the stage: only an editable
  // element gets IME composition and soft-keyboard input.
  const keyboardRef = useRef<HTMLTextAreaElement | null>(null);
  const mountedRef = useRef(true);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const headerRef = useRef<FrameHeader | null>(null);
  const drawnRef = useRef<Rect>({ x: 0, y: 0, width: 0, height: 0 });
  const bitmapRef = useRef<ImageBitmap | null>(null);
  const decodingRef = useRef(false);
  const pendingFrameRef = useRef<{ header: FrameHeader; jpeg: Uint8Array } | null>(null);
  const autoReconnectsRef = useRef(0);
  const lastInputRef = useRef(0);
  const tabsRef = useRef<{ tabs: BrowserTabInfo[]; active: string } | null>(null);
  const agentTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const noticeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const moveRef = useRef<Record<string, unknown> | null>(null);
  const wheelRef = useRef<{
    x: number;
    y: number;
    dx: number;
    dy: number;
    modifiers: number;
  } | null>(null);
  const rafRef = useRef<number | null>(null);
  const lastDownRef = useRef<{ at: number; x: number; y: number; count: number } | null>(null);

  const send = useCallback((msg: Record<string, unknown>) => {
    const ws = wsRef.current;
    if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }, []);

  const draw = useCallback(() => {
    const canvas = canvasRef.current;
    const header = headerRef.current;
    const bitmap = bitmapRef.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;
    const dpr = window.devicePixelRatio || 1;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    if (!header || !bitmap) return;
    const rect = fitRect(canvas.width / dpr, canvas.height / dpr, header.width, header.height);
    drawnRef.current = rect;
    ctx.drawImage(bitmap, rect.x * dpr, rect.y * dpr, rect.width * dpr, rect.height * dpr);
  }, []);

  const showFrame = useCallback(
    (frame: { header: FrameHeader; jpeg: Uint8Array }) => {
      headerRef.current = frame.header;
      setPageSize((prev) =>
        prev?.width === frame.header.width && prev.height === frame.header.height
          ? prev
          : { width: frame.header.width, height: frame.header.height },
      );
      const ack = () => send({ type: 'ack', seq: frame.header.seq });
      if (typeof createImageBitmap !== 'function') {
        ack();
        return;
      }
      if (decodingRef.current) {
        // Only the newest frame is worth drawing; its ack covers this one.
        pendingFrameRef.current = frame;
        return;
      }
      decodingRef.current = true;
      createImageBitmap(new Blob([frame.jpeg as Uint8Array<ArrayBuffer>], { type: 'image/jpeg' }))
        .then((bitmap) => {
          if (!mountedRef.current) {
            bitmap.close();
            return;
          }
          bitmapRef.current?.close();
          bitmapRef.current = bitmap;
          draw();
        })
        .catch((error: unknown) => logger.error('Browser frame decode failed', 'BrowserTab', error))
        .finally(() => {
          ack();
          decodingRef.current = false;
          const next = pendingFrameRef.current;
          pendingFrameRef.current = null;
          if (next) showFrame(next);
        });
    },
    [draw, send],
  );

  const flashNotice = useCallback((message: string) => {
    setNotice(message);
    if (noticeTimerRef.current) clearTimeout(noticeTimerRef.current);
    noticeTimerRef.current = setTimeout(() => setNotice(null), NOTICE_MS);
  }, []);

  const onTabs = useCallback((next: { tabs: BrowserTabInfo[]; active: string }) => {
    const prev = tabsRef.current;
    tabsRef.current = next;
    setTabs(next.tabs);
    setActiveId(next.active);
    if (!prev) return;
    const urlOf = (t: { tabs: BrowserTabInfo[]; active: string }) =>
      t.tabs.find((tab) => tab.id === t.active)?.url;
    const changed = prev.active !== next.active || urlOf(prev) !== urlOf(next);
    if (changed && Date.now() - lastInputRef.current > AGENT_QUIET_MS) {
      setAgentActive(true);
      if (agentTimerRef.current) clearTimeout(agentTimerRef.current);
      agentTimerRef.current = setTimeout(() => setAgentActive(false), AGENT_BADGE_MS);
    }
  }, []);

  const reportScreen = useCallback(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const dpr = window.devicePixelRatio || 1;
    const width = Math.round(stage.clientWidth * dpr);
    const height = Math.round(stage.clientHeight * dpr);
    if (width > 0 && height > 0) send({ type: 'screen', width, height });
  }, [send]);

  useEffect(() => {
    if (!sandboxId || !connected) {
      setConnection('idle');
      return;
    }
    setConnection('connecting');
    setCloseReason(null);
    let cancelled = false;
    let ws: WebSocket | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;

    const onMessage = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer) {
        const frame = parseFrame(event.data);
        if (frame) showFrame(frame);
        return;
      }
      if (typeof event.data !== 'string') return;
      const msg = parseServerMessage(event.data);
      if (!msg) return;
      switch (msg.type) {
        case 'status':
          if (msg.state === 'ready') autoReconnectsRef.current = 0;
          setConnection(msg.state);
          break;
        case 'tabs':
          onTabs(msg);
          break;
        case 'nav':
          setNav({ canGoBack: msg.canGoBack, canGoForward: msg.canGoForward });
          break;
        case 'loading':
          setLoading(msg.loading);
          break;
        case 'dialog':
          setDialog(
            msg.open
              ? {
                  dialogType: msg.dialogType ?? 'alert',
                  message: msg.message ?? '',
                  defaultPrompt: msg.defaultPrompt ?? '',
                }
              : null,
          );
          setPromptText(msg.defaultPrompt ?? '');
          break;
        case 'error':
          flashNotice(msg.message);
          break;
      }
    };

    const onClose = (event: CloseEvent) => {
      if (wsRef.current === ws) wsRef.current = null;
      if (cancelled) return;
      if (FATAL_CLOSE_CODES.has(event.code)) {
        setCloseReason(event.reason || null);
        setConnection('error');
        return;
      }
      if (event.code !== WS_CLOSE_NORMAL && autoReconnectsRef.current < MAX_AUTO_RECONNECTS) {
        const delay = RECONNECT_BASE_DELAY_MS * 2 ** autoReconnectsRef.current;
        autoReconnectsRef.current += 1;
        setConnection('reconnecting');
        retryTimer = setTimeout(() => setConnectAttempt((n) => n + 1), delay);
        return;
      }
      setConnection('disconnected');
    };

    // A fresh token, like the terminal: the cached one may have expired.
    void apiClient
      .getValidToken()
      .then((token) => {
        if (cancelled) return;
        if (!token) throw new Error('Missing browser auth token');
        const socket = new WebSocket(
          `${API_WS_URL}/sandbox/${encodeURIComponent(sandboxId)}/browser/ws`,
        );
        ws = socket;
        socket.binaryType = 'arraybuffer';
        wsRef.current = socket;
        socket.addEventListener('open', () => {
          socket.send(JSON.stringify({ type: 'auth', token }));
          reportScreen();
        });
        socket.addEventListener('message', onMessage);
        socket.addEventListener('close', onClose);
        socket.addEventListener('error', () => logger.error('Browser socket error', 'BrowserTab'));
      })
      .catch((error: unknown) => {
        if (cancelled) return;
        logger.error('Browser auth token mint failed', 'BrowserTab', error);
        setCloseReason('Browser authentication failed');
        setConnection('error');
      });

    return () => {
      cancelled = true;
      if (retryTimer) clearTimeout(retryTimer);
      ws?.close();
      if (wsRef.current === ws) wsRef.current = null;
      tabsRef.current = null;
      setDialog(null);
      setLoading(false);
    };
  }, [sandboxId, connected, connectAttempt, showFrame, onTabs, flashNotice, reportScreen]);

  // Keep the canvas at the stage's device-pixel size and tell the server,
  // so frames are sent no larger than they are drawn.
  useEffect(() => {
    const stage = stageRef.current;
    const canvas = canvasRef.current;
    if (!stage || !canvas || typeof ResizeObserver === 'undefined') return;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const observer = new ResizeObserver(() => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = Math.round(stage.clientWidth * dpr);
      canvas.height = Math.round(stage.clientHeight * dpr);
      draw();
      if (timer) clearTimeout(timer);
      timer = setTimeout(reportScreen, SCREEN_REPORT_DELAY_MS);
    });
    observer.observe(stage);
    return () => {
      observer.disconnect();
      if (timer) clearTimeout(timer);
    };
  }, [draw, reportScreen]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      bitmapRef.current?.close();
      bitmapRef.current = null;
      if (agentTimerRef.current) clearTimeout(agentTimerRef.current);
      if (noticeTimerRef.current) clearTimeout(noticeTimerRef.current);
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current);
    };
  }, []);

  const pagePoint = (clientX: number, clientY: number) => {
    const canvas = canvasRef.current;
    const header = headerRef.current;
    if (!canvas || !header) return null;
    const box = canvas.getBoundingClientRect();
    return toPagePoint(clientX - box.left, clientY - box.top, drawnRef.current, header);
  };

  // Moves and wheel turns are sent at most once per animation frame.
  const flushPointer = useCallback(() => {
    rafRef.current = null;
    if (moveRef.current) {
      send(moveRef.current);
      moveRef.current = null;
    }
    const wheel = wheelRef.current;
    if (wheel) {
      send({
        type: 'wheel',
        x: wheel.x,
        y: wheel.y,
        deltaX: wheel.dx,
        deltaY: wheel.dy,
        modifiers: wheel.modifiers,
      });
      wheelRef.current = null;
    }
  }, [send]);

  const scheduleFlush = useCallback(() => {
    if (rafRef.current === null) rafRef.current = requestAnimationFrame(flushPointer);
  }, [flushPointer]);

  const markInput = () => {
    lastInputRef.current = Date.now();
    setAgentActive(false);
  };

  const onPointerDown = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const p = pagePoint(e.clientX, e.clientY);
    if (!p?.inside) return;
    e.preventDefault();
    // A tap would pop up the soft keyboard; touch users open it with the
    // keyboard button instead.
    if (e.pointerType !== 'touch') keyboardRef.current?.focus({ preventScroll: true });
    e.currentTarget.setPointerCapture?.(e.pointerId);
    markInput();
    flushPointer();
    // Pointer events carry no click count; count repeats like the OS does.
    const last = lastDownRef.current;
    const now = Date.now();
    const repeat =
      last &&
      now - last.at < DOUBLE_CLICK_MS &&
      Math.abs(last.x - e.clientX) < DOUBLE_CLICK_SLOP_PX &&
      Math.abs(last.y - e.clientY) < DOUBLE_CLICK_SLOP_PX;
    const clickCount = repeat ? Math.min(last.count + 1, 3) : 1;
    lastDownRef.current = { at: now, x: e.clientX, y: e.clientY, count: clickCount };
    send({
      type: 'mouse',
      event: 'down',
      x: p.x,
      y: p.y,
      button: mouseButtonName(e.button),
      buttons: e.buttons,
      clickCount,
      modifiers: cdpModifiers(e),
    });
  };

  const onPointerMove = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const p = pagePoint(e.clientX, e.clientY);
    if (!p) return;
    moveRef.current = {
      type: 'mouse',
      event: 'move',
      x: p.x,
      y: p.y,
      buttons: e.buttons,
      modifiers: cdpModifiers(e),
    };
    scheduleFlush();
  };

  const onPointerUp = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const p = pagePoint(e.clientX, e.clientY);
    if (!p) return;
    flushPointer();
    markInput();
    send({
      type: 'mouse',
      event: 'up',
      x: p.x,
      y: p.y,
      button: mouseButtonName(e.button),
      buttons: e.buttons,
      clickCount: lastDownRef.current?.count ?? 1,
      modifiers: cdpModifiers(e),
    });
  };

  // React's wheel listener is passive; scrolling the page needs preventDefault.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const onWheel = (e: WheelEvent) => {
      const p = pagePoint(e.clientX, e.clientY);
      if (!p?.inside) return;
      e.preventDefault();
      lastInputRef.current = Date.now();
      const { dx, dy } = wheelDelta(e, headerRef.current?.height ?? 800);
      const prev = wheelRef.current;
      wheelRef.current = {
        x: p.x,
        y: p.y,
        dx: (prev?.dx ?? 0) + dx,
        dy: (prev?.dy ?? 0) + dy,
        modifiers: cdpModifiers(e),
      };
      scheduleFlush();
    };
    canvas.addEventListener('wheel', onWheel, { passive: false });
    return () => canvas.removeEventListener('wheel', onWheel);
  }, [scheduleFlush]);

  const onKey = (e: ReactKeyboardEvent<HTMLTextAreaElement>, event: 'down' | 'up') => {
    // Composition and soft keyboards ('Unidentified', 229) arrive as text
    // through compositionend/input instead.
    if (
      e.nativeEvent.isComposing ||
      e.keyCode === 229 ||
      ['Process', 'Dead', 'Unidentified'].includes(e.key)
    ) {
      return;
    }
    if (isLocalShortcut(e)) return;
    e.preventDefault();
    markInput();
    send({
      type: 'key',
      event,
      key: e.key,
      code: e.code,
      keyCode: e.keyCode,
      location: e.location,
      repeat: e.repeat,
      modifiers: cdpModifiers(e),
    });
  };

  const sendTyped = (text: string) => {
    if (!text) return;
    markInput();
    send({ type: 'text', text });
  };

  const onCompositionEnd = (e: ReactCompositionEvent<HTMLTextAreaElement>) => {
    sendTyped(e.data);
    e.currentTarget.value = '';
  };

  // Text that bypassed keydown (soft keyboards, dictation); composition
  // ends in onCompositionEnd.
  const onInput = (e: ReactFormEvent<HTMLTextAreaElement>) => {
    if ((e.nativeEvent as InputEvent).isComposing) return;
    sendTyped(e.currentTarget.value);
    e.currentTarget.value = '';
  };

  const onPaste = (e: ReactClipboardEvent<HTMLTextAreaElement>) => {
    const text = e.clipboardData.getData('text/plain');
    if (!text) return;
    e.preventDefault();
    markInput();
    send({ type: 'text', text });
  };

  // Soft keyboards often send Backspace and Enter as keyCode 229 with no key;
  // their beforeinput still says what they mean.
  useEffect(() => {
    const keyboard = keyboardRef.current;
    if (!keyboard) return;
    const onBeforeInput = (e: InputEvent) => {
      const key =
        e.inputType === 'deleteContentBackward'
          ? { key: 'Backspace', code: 'Backspace', keyCode: 8 }
          : e.inputType === 'insertLineBreak' || e.inputType === 'insertParagraph'
            ? { key: 'Enter', code: 'Enter', keyCode: 13 }
            : null;
      if (!key) return;
      e.preventDefault();
      lastInputRef.current = Date.now();
      for (const event of ['down', 'up']) {
        send({ type: 'key', event, ...key, location: 0, repeat: false, modifiers: 0 });
      }
    };
    keyboard.addEventListener('beforeinput', onBeforeInput);
    return () => keyboard.removeEventListener('beforeinput', onBeforeInput);
  }, [send]);

  // Reset goes over HTTP, so it works when no viewer session is up (e.g. a
  // page broke every connection); the viewer then reconnects.
  const resetBrowser = async () => {
    if (!sandboxId) return;
    markInput();
    setResetting(true);
    try {
      await apiClient.post(`/sandbox/${encodeURIComponent(sandboxId)}/browser/reset`);
    } catch (error) {
      logger.error('Browser reset failed', 'BrowserTab', error);
      flashNotice('Could not reset the browser');
    } finally {
      setResetting(false);
      autoReconnectsRef.current = 0;
      setConnectAttempt((n) => n + 1);
    }
  };

  const command = (msg: Record<string, unknown>) => {
    markInput();
    send(msg);
  };

  const activeTab = tabs.find((tab) => tab.id === activeId);
  const ready = connection === 'ready';
  const preset = VIEWPORT_PRESETS.find(
    (p) => p.width === pageSize?.width && p.height === pageSize?.height,
  );

  const overlayMessage = resetting
    ? 'Resetting the browser…'
    : connection === 'connecting'
      ? 'Connecting to the browser…'
      : connection === 'starting'
        ? 'Starting the browser…'
        : connection === 'restarting'
          ? 'Restarting the browser…'
          : connection === 'reconnecting'
            ? 'Connection lost — reconnecting…'
            : connection === 'error'
              ? (closeReason ?? 'Browser unavailable')
              : connection === 'disconnected'
                ? 'Browser disconnected'
                : null;
  const canReconnect = connection === 'error' || connection === 'disconnected';

  return (
    <div className={styles['browser-tab']}>
      <div className={styles.tabstrip} role="tablist" aria-label="Browser tabs">
        {tabs.map((tab) => (
          <Button
            variant="unstyled"
            key={tab.id}
            role="tab"
            aria-selected={tab.id === activeId}
            className={styles.tab}
            title={tab.url}
            onClick={() => command({ type: 'tab.switch', id: tab.id })}
          >
            <span className={styles['tab-label']}>{tabLabel(tab)}</span>
            <span
              role="button"
              aria-label={`Close ${tabLabel(tab)}`}
              className={styles['tab-close']}
              onClick={(e) => {
                e.stopPropagation();
                command({ type: 'tab.close', id: tab.id });
              }}
            >
              <X className={styles['tab-close-icon']} />
            </span>
          </Button>
        ))}
        <Button
          variant="unstyled"
          className={styles['icon-button']}
          aria-label="New tab"
          disabled={!ready}
          onClick={() => command({ type: 'tab.new' })}
        >
          <Plus className={styles.icon} />
        </Button>
      </div>

      <div className={styles.toolbar}>
        <Button
          variant="unstyled"
          className={styles['icon-button']}
          aria-label="Back"
          disabled={!ready || !nav.canGoBack}
          onClick={() => command({ type: 'back' })}
        >
          <ArrowLeft className={styles.icon} />
        </Button>
        <Button
          variant="unstyled"
          className={styles['icon-button']}
          aria-label="Forward"
          disabled={!ready || !nav.canGoForward}
          onClick={() => command({ type: 'forward' })}
        >
          <ArrowRight className={styles.icon} />
        </Button>
        <Button
          variant="unstyled"
          className={styles['icon-button']}
          aria-label={loading ? 'Stop' : 'Reload'}
          disabled={!ready || !activeTab}
          onClick={() => command({ type: loading ? 'stop' : 'reload' })}
        >
          {loading ? <X className={styles.icon} /> : <RotateCw className={styles.icon} />}
        </Button>
        <input
          className={styles.url}
          aria-label="Address"
          placeholder="localhost:3000"
          spellCheck={false}
          disabled={!ready || !activeTab}
          value={urlDraft ?? (activeTab?.url === 'about:blank' ? '' : (activeTab?.url ?? ''))}
          onChange={(e) => setUrlDraft(e.target.value)}
          onFocus={(e) => e.currentTarget.select()}
          onBlur={() => setUrlDraft(null)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && urlDraft?.trim()) {
              command({ type: 'navigate', url: urlDraft.trim() });
              setUrlDraft(null);
              e.currentTarget.blur();
            } else if (e.key === 'Escape') {
              setUrlDraft(null);
              e.currentTarget.blur();
            }
          }}
        />
        {agentActive && (
          <span className={styles.agent} title="The agent is using this browser">
            <Bot className={styles.icon} />
            Agent
          </span>
        )}
        <select
          className={styles.viewport}
          aria-label="Window size"
          disabled={!ready || !activeTab}
          value={preset ? `${preset.width}x${preset.height}` : 'custom'}
          onChange={(e) => {
            const stage = stageRef.current;
            const [width, height] =
              e.target.value === 'fit' && stage
                ? [stage.clientWidth, stage.clientHeight]
                : e.target.value.split('x').map(Number);
            if (width > 0 && height > 0) command({ type: 'viewport', width, height });
          }}
        >
          {!preset && (
            <option value="custom">
              {pageSize ? `${Math.round(pageSize.width)}×${Math.round(pageSize.height)}` : 'Size'}
            </option>
          )}
          {VIEWPORT_PRESETS.map((p) => (
            <option key={p.label} value={`${p.width}x${p.height}`}>
              {p.label}
            </option>
          ))}
          <option value="fit">Fit to panel</option>
        </select>
        <Button
          variant="unstyled"
          className={styles['icon-button']}
          aria-label="Reset browser"
          title="Reset browser: close it and clear cookies, storage and history"
          disabled={!sandboxId || resetting}
          onClick={() => setConfirmReset(true)}
        >
          <Trash2 className={styles.icon} />
        </Button>
        <Button
          variant="unstyled"
          className={clsx(styles['icon-button'], styles['keyboard-button'])}
          aria-label="Show keyboard"
          disabled={!ready}
          onClick={() => keyboardRef.current?.focus({ preventScroll: true })}
        >
          <Keyboard className={styles.icon} />
        </Button>
      </div>

      <div ref={stageRef} className={styles.stage} aria-label="Browser page">
        <textarea
          ref={keyboardRef}
          className={styles.keyboard}
          aria-label="Browser keyboard"
          autoCapitalize="off"
          autoComplete="off"
          autoCorrect="off"
          spellCheck={false}
          onKeyDown={(e) => onKey(e, 'down')}
          onKeyUp={(e) => onKey(e, 'up')}
          onPaste={onPaste}
          onCompositionEnd={onCompositionEnd}
          onInput={onInput}
        />
        <canvas
          ref={canvasRef}
          className={styles.canvas}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onContextMenu={(e) => e.preventDefault()}
        />
        {ready && tabs.length === 0 && (
          <div className={styles.overlay}>
            <div className={styles['overlay-message']}>No open tabs</div>
            <Button variant="outline" size="sm" onClick={() => command({ type: 'tab.new' })}>
              New tab
            </Button>
          </div>
        )}
        {dialog && (
          <div className={styles['dialog-backdrop']}>
            <div className={styles.dialog} role="dialog" aria-label="Page dialog">
              <p className={styles['dialog-message']}>{dialog.message || 'The page asks:'}</p>
              {dialog.dialogType === 'prompt' && (
                <input
                  className={styles.url}
                  aria-label="Dialog answer"
                  value={promptText}
                  onChange={(e) => setPromptText(e.target.value)}
                />
              )}
              <div className={styles['dialog-actions']}>
                {dialog.dialogType !== 'alert' && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => command({ type: 'dialog', accept: false })}
                  >
                    Cancel
                  </Button>
                )}
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => command({ type: 'dialog', accept: true, promptText })}
                >
                  OK
                </Button>
              </div>
            </div>
          </div>
        )}
        {notice && <div className={styles.notice}>{notice}</div>}
        {isVisible && overlayMessage && (
          <div className={clsx(styles.overlay, styles['overlay--opaque'])}>
            <div className={styles['overlay-message']}>{overlayMessage}</div>
            {canReconnect && !resetting && (
              <div className={styles['overlay-actions']}>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    autoReconnectsRef.current = 0;
                    setConnectAttempt((n) => n + 1);
                  }}
                >
                  Reconnect
                </Button>
                {sandboxId && (
                  <Button variant="ghost" size="sm" onClick={() => setConfirmReset(true)}>
                    Reset browser
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </div>

      <ConfirmDialog
        isOpen={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={() => void resetBrowser()}
        title="Reset the browser?"
        message="This closes every tab and clears the browser's cookies, logins, storage and history, for you and the agent."
        confirmLabel="Reset"
      />
    </div>
  );
}
