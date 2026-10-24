import { describe, expect, it } from 'vitest';

import {
  cdpModifiers,
  fitRect,
  isLocalShortcut,
  mouseButtonName,
  parseFrame,
  parseServerMessage,
  tabLabel,
  toPagePoint,
  wheelDelta,
} from './browserProtocol';

function frameBuffer(header: object, payload: number[]): ArrayBuffer {
  const head = new TextEncoder().encode(JSON.stringify(header));
  const buf = new ArrayBuffer(4 + head.length + payload.length);
  new DataView(buf).setUint32(0, head.length);
  new Uint8Array(buf, 4).set(head);
  new Uint8Array(buf, 4 + head.length).set(payload);
  return buf;
}

describe('parseFrame', () => {
  it('splits the header from the JPEG', () => {
    const frame = parseFrame(frameBuffer({ seq: 7, width: 1280, height: 800 }, [0xff, 0xd8, 1]));
    expect(frame?.header).toEqual({ seq: 7, width: 1280, height: 800 });
    expect(Array.from(frame?.jpeg ?? [])).toEqual([0xff, 0xd8, 1]);
  });

  it('rejects truncated or malformed frames', () => {
    expect(parseFrame(new ArrayBuffer(2))).toBeNull();
    const truncated = new ArrayBuffer(8);
    new DataView(truncated).setUint32(0, 100);
    expect(parseFrame(truncated)).toBeNull();
    expect(parseFrame(frameBuffer({ seq: 1, width: 0, height: 800 }, []))).toBeNull();
  });
});

describe('parseServerMessage', () => {
  it('accepts known messages only', () => {
    expect(parseServerMessage('{"type":"status","state":"ready"}')).toEqual({
      type: 'status',
      state: 'ready',
    });
    expect(parseServerMessage('{"type":"tabs","tabs":[],"active":""}')).not.toBeNull();
    expect(parseServerMessage('{"type":"tabs"}')).toBeNull();
    expect(parseServerMessage('{"type":"evil"}')).toBeNull();
    expect(parseServerMessage('not json')).toBeNull();
  });
});

describe('coordinate mapping', () => {
  it('letterboxes a wide page in a tall panel', () => {
    // 1280x800 into 640x800: scale 0.5, centered vertically.
    expect(fitRect(640, 800, 1280, 800)).toEqual({ x: 0, y: 200, width: 640, height: 400 });
    // Pillarboxed the other way.
    expect(fitRect(1000, 400, 800, 800)).toEqual({ x: 300, y: 0, width: 400, height: 400 });
    expect(fitRect(0, 400, 800, 800).width).toBe(0);
  });

  it('maps stage points to page CSS pixels', () => {
    const drawn = fitRect(640, 800, 1280, 800);
    const page = { width: 1280, height: 800 };
    expect(toPagePoint(0, 200, drawn, page)).toEqual({ x: 0, y: 0, inside: true });
    expect(toPagePoint(320, 400, drawn, page)).toEqual({ x: 640, y: 400, inside: true });
    expect(toPagePoint(640, 600, drawn, page)).toEqual({ x: 1280, y: 800, inside: true });
    // In the letterbox: outside, clamped to the edge (ends drags cleanly).
    expect(toPagePoint(100, 100, drawn, page)).toEqual({ x: 200, y: 0, inside: false });
  });

  it('handles a scaled-down frame of a small viewport', () => {
    // A 390x844 phone viewport drawn into a 1000x500 stage.
    const drawn = fitRect(1000, 500, 390, 844);
    const p = toPagePoint(drawn.x + drawn.width / 2, drawn.y + drawn.height / 4, drawn, {
      width: 390,
      height: 844,
    });
    expect(p).toEqual({ x: 195, y: 211, inside: true });
  });
});

describe('input helpers', () => {
  it('builds CDP modifier bits', () => {
    expect(cdpModifiers({ altKey: true, ctrlKey: false, metaKey: false, shiftKey: false })).toBe(1);
    expect(cdpModifiers({ altKey: false, ctrlKey: true, metaKey: true, shiftKey: true })).toBe(14);
  });

  it('names mouse buttons', () => {
    expect(mouseButtonName(0)).toBe('left');
    expect(mouseButtonName(1)).toBe('middle');
    expect(mouseButtonName(2)).toBe('right');
    expect(mouseButtonName(-1)).toBe('none');
  });

  it('converts wheel deltas to pixels', () => {
    expect(wheelDelta({ deltaX: 0, deltaY: 120, deltaMode: 0 }, 800)).toEqual({ dx: 0, dy: 120 });
    expect(wheelDelta({ deltaX: 1, deltaY: 3, deltaMode: 1 }, 800)).toEqual({ dx: 40, dy: 120 });
    expect(wheelDelta({ deltaX: 0, deltaY: 1, deltaMode: 2 }, 800)).toEqual({ dx: 0, dy: 800 });
  });

  it('leaves paste shortcuts to the local browser', () => {
    expect(isLocalShortcut({ key: 'v', ctrlKey: true, metaKey: false })).toBe(true);
    expect(isLocalShortcut({ key: 'v', ctrlKey: false, metaKey: true })).toBe(true);
    expect(isLocalShortcut({ key: 'v', ctrlKey: false, metaKey: false })).toBe(false);
    expect(isLocalShortcut({ key: 'a', ctrlKey: true, metaKey: false })).toBe(false);
  });

  it('labels tabs', () => {
    expect(tabLabel({ id: '1', title: 'App', url: 'http://localhost:3000/' })).toBe('App');
    expect(tabLabel({ id: '1', title: '', url: 'about:blank' })).toBe('New tab');
    expect(
      tabLabel({ id: '1', title: 'http://localhost:3000/x', url: 'http://localhost:3000/x' }),
    ).toBe('localhost:3000/x');
  });
});
