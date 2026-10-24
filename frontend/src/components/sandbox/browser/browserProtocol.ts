// Client side of the browser viewer protocol (backend/internal/browserview).
// The server sends JSON text messages and binary screencast frames: a 4-byte
// big-endian header length, a JSON FrameHeader, then the JPEG.

export interface FrameHeader {
  seq: number;
  // Page viewport the frame shows, in CSS pixels.
  width: number;
  height: number;
}

export interface BrowserTabInfo {
  id: string;
  title: string;
  url: string;
}

export type ServerMessage =
  | { type: 'status'; state: 'starting' | 'ready' | 'restarting' }
  | { type: 'tabs'; tabs: BrowserTabInfo[]; active: string }
  | { type: 'nav'; canGoBack: boolean; canGoForward: boolean }
  | { type: 'loading'; loading: boolean }
  | {
      type: 'dialog';
      open: boolean;
      dialogType?: string;
      message?: string;
      defaultPrompt?: string;
    }
  | { type: 'error'; message: string }
  | { type: 'ping' };

const MESSAGE_TYPES = new Set(['status', 'tabs', 'nav', 'loading', 'dialog', 'error', 'ping']);

export function parseServerMessage(data: string): ServerMessage | null {
  try {
    const msg = JSON.parse(data) as { type?: unknown };
    if (typeof msg.type !== 'string' || !MESSAGE_TYPES.has(msg.type)) return null;
    if (msg.type === 'tabs' && !Array.isArray((msg as { tabs?: unknown }).tabs)) return null;
    return msg as ServerMessage;
  } catch {
    return null;
  }
}

export function parseFrame(buf: ArrayBuffer): { header: FrameHeader; jpeg: Uint8Array } | null {
  if (buf.byteLength < 4) return null;
  const headerLength = new DataView(buf).getUint32(0);
  if (4 + headerLength > buf.byteLength) return null;
  try {
    const header = JSON.parse(
      new TextDecoder().decode(new Uint8Array(buf, 4, headerLength)),
    ) as FrameHeader;
    if (typeof header.seq !== 'number' || !(header.width > 0) || !(header.height > 0)) return null;
    return { header, jpeg: new Uint8Array(buf, 4 + headerLength) };
  } catch {
    return null;
  }
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

// fitRect scales content into a box keeping its aspect ratio, centered.
export function fitRect(boxW: number, boxH: number, contentW: number, contentH: number): Rect {
  if (boxW <= 0 || boxH <= 0 || contentW <= 0 || contentH <= 0) {
    return { x: 0, y: 0, width: 0, height: 0 };
  }
  const scale = Math.min(boxW / contentW, boxH / contentH);
  const width = contentW * scale;
  const height = contentH * scale;
  return { x: (boxW - width) / 2, y: (boxH - height) / 2, width, height };
}

// toPagePoint maps a point in the stage (CSS pixels from its top-left) to
// page CSS pixels, given where the frame is drawn. Points in the letterbox
// are clamped to the page edge.
export function toPagePoint(
  stageX: number,
  stageY: number,
  drawn: Rect,
  page: { width: number; height: number },
): { x: number; y: number; inside: boolean } {
  if (drawn.width <= 0 || drawn.height <= 0) return { x: 0, y: 0, inside: false };
  const rx = (stageX - drawn.x) / drawn.width;
  const ry = (stageY - drawn.y) / drawn.height;
  const inside = rx >= 0 && rx <= 1 && ry >= 0 && ry <= 1;
  const clamp = (v: number) => Math.min(1, Math.max(0, v));
  return {
    x: Math.round(clamp(rx) * page.width * 100) / 100,
    y: Math.round(clamp(ry) * page.height * 100) / 100,
    inside,
  };
}

// CDP modifier bits.
export function cdpModifiers(e: {
  altKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  shiftKey: boolean;
}): number {
  return (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0);
}

const BUTTON_NAMES = ['left', 'middle', 'right', 'back', 'forward'] as const;

export function mouseButtonName(button: number): string {
  return BUTTON_NAMES[button] ?? 'none';
}

const LINE_HEIGHT_PX = 40;

// wheelDelta converts a wheel event's deltas to pixels.
export function wheelDelta(
  e: { deltaX: number; deltaY: number; deltaMode: number },
  pageHeight: number,
): { dx: number; dy: number } {
  const unit = e.deltaMode === 1 ? LINE_HEIGHT_PX : e.deltaMode === 2 ? pageHeight : 1;
  return { dx: e.deltaX * unit, dy: e.deltaY * unit };
}

// Keys the viewer leaves to the user's own browser: pasting goes through the
// paste event (a prevented Ctrl+V never fires one).
export function isLocalShortcut(e: { key: string; ctrlKey: boolean; metaKey: boolean }): boolean {
  return (e.ctrlKey || e.metaKey) && (e.key === 'v' || e.key === 'V');
}

export const VIEWPORT_PRESETS = [
  { label: 'Desktop 1280×800', width: 1280, height: 800 },
  { label: 'Laptop 1440×900', width: 1440, height: 900 },
  { label: 'Full HD 1920×1080', width: 1920, height: 1080 },
  { label: 'Tablet 768×1024', width: 768, height: 1024 },
  { label: 'Phone 390×844', width: 390, height: 844 },
] as const;

export function tabLabel(tab: BrowserTabInfo): string {
  if (tab.title && tab.title !== tab.url) return tab.title;
  if (!tab.url || tab.url === 'about:blank') return 'New tab';
  return tab.url.replace(/^https?:\/\//, '');
}
