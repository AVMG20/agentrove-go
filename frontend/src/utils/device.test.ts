// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  applyLayoutAttributes,
  isCoarsePointer,
  readLayoutMode,
  subscribeLayoutMode,
  TOUCH_MEDIA_QUERY,
} from './device';
import {
  setViewport,
  stubMatchMedia,
  stubScreen,
  stubTouch,
  type MatchMediaStub,
  type ScreenStub,
} from '@/test-utils/device';

let media: MatchMediaStub | undefined;
let screenStub: ScreenStub | undefined;

afterEach(() => {
  media?.restore();
  screenStub?.restore();
  media = undefined;
  screenStub = undefined;
  setViewport(1024, 768);
  document.documentElement.removeAttribute('data-mobile');
  document.documentElement.removeAttribute('data-phone-landscape');
});

describe('readLayoutMode', () => {
  it('gives a phone the phone layout either way up', () => {
    media = stubTouch();
    screenStub = stubScreen(390, 844, 'portrait-primary');
    setViewport(390, 844);
    expect(readLayoutMode()).toEqual({ mobile: true, phoneLandscape: false });

    screenStub.setOrientation('landscape-primary');
    setViewport(844, 390);
    expect(readLayoutMode()).toEqual({ mobile: true, phoneLandscape: true });
  });

  it('falls back to the viewport sides without screen.orientation (older iOS)', () => {
    media = stubTouch();
    screenStub = stubScreen(390, 844);
    setViewport(844, 390);
    expect(readLayoutMode()).toEqual({ mobile: true, phoneLandscape: true });
  });

  it('keeps a tablet on the desktop layout while the keyboard shrinks its viewport', () => {
    // Firefox Android resizes the layout viewport for the keyboard; the screen stays.
    media = stubTouch();
    screenStub = stubScreen(1280, 800, 'landscape-primary');
    setViewport(1280, 720);
    expect(readLayoutMode().mobile).toBe(false);
    setViewport(1280, 380);
    expect(readLayoutMode()).toEqual({ mobile: false, phoneLandscape: false });
  });

  it('keeps a short window on a large touch screen on the desktop layout', () => {
    media = stubTouch();
    screenStub = stubScreen(1366, 768, 'landscape-primary');
    setViewport(1366, 460);
    expect(readLayoutMode().mobile).toBe(false);
  });

  it('gives a narrow window the phone layout, mouse or not', () => {
    media = stubTouch(false);
    screenStub = stubScreen(1440, 900, 'landscape-primary');
    setViewport(600, 900);
    expect(readLayoutMode()).toEqual({ mobile: true, phoneLandscape: false });
    setViewport(1440, 900);
    expect(readLayoutMode().mobile).toBe(false);
  });

  it('never calls a mouse-driven small screen a phone', () => {
    media = stubTouch(false);
    screenStub = stubScreen(390, 844, 'landscape-primary');
    setViewport(844, 390);
    expect(readLayoutMode()).toEqual({ mobile: false, phoneLandscape: false });
  });

  it('uses the width rule alone without matchMedia', () => {
    setViewport(500, 800);
    expect(isCoarsePointer()).toBe(false);
    expect(readLayoutMode().mobile).toBe(true);
  });
});

describe('applyLayoutAttributes', () => {
  it('mirrors the mode on <html> for the CSS', () => {
    applyLayoutAttributes({ mobile: true, phoneLandscape: true });
    expect(document.documentElement.hasAttribute('data-mobile')).toBe(true);
    expect(document.documentElement.hasAttribute('data-phone-landscape')).toBe(true);
    applyLayoutAttributes({ mobile: false, phoneLandscape: false });
    expect(document.documentElement.hasAttribute('data-mobile')).toBe(false);
    expect(document.documentElement.hasAttribute('data-phone-landscape')).toBe(false);
  });
});

describe('subscribeLayoutMode', () => {
  it('fires on resize, rotation and a pointer change, until unsubscribed', () => {
    media = stubMatchMedia({ [TOUCH_MEDIA_QUERY]: true });
    screenStub = stubScreen(390, 844, 'portrait-primary');
    const onChange = vi.fn();
    const unsubscribe = subscribeLayoutMode(onChange);

    window.dispatchEvent(new Event('resize'));
    screenStub.setOrientation('landscape-primary');
    media.set(TOUCH_MEDIA_QUERY, false);
    expect(onChange).toHaveBeenCalledTimes(3);

    unsubscribe();
    window.dispatchEvent(new Event('resize'));
    media.set(TOUCH_MEDIA_QUERY, true);
    expect(onChange).toHaveBeenCalledTimes(3);
  });
});
