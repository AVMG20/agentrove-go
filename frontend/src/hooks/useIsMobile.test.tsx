// @vitest-environment happy-dom

import { afterEach, describe, expect, it } from 'vitest';
import { act, cleanup, renderHook } from '@testing-library/react';
import { TOUCH_MEDIA_QUERY } from '@/utils/device';
import {
  setViewport,
  stubMatchMedia,
  stubScreen,
  type MatchMediaStub,
  type ScreenStub,
} from '@/test-utils/device';
import { useIsMobile } from './useIsMobile';

let media: MatchMediaStub | undefined;
let screenStub: ScreenStub | undefined;

afterEach(() => {
  cleanup();
  media?.restore();
  screenStub?.restore();
  setViewport(1024, 768);
});

describe('useIsMobile', () => {
  it('follows window resizes', () => {
    setViewport(1024, 768);
    const { result } = renderHook(() => useIsMobile());
    expect(result.current).toBe(false);
    act(() => {
      setViewport(500, 768);
      window.dispatchEvent(new Event('resize'));
    });
    expect(result.current).toBe(true);
  });

  it('follows the main pointer changing without a resize', () => {
    media = stubMatchMedia({ [TOUCH_MEDIA_QUERY]: false });
    screenStub = stubScreen(844, 390, 'landscape-primary');
    setViewport(844, 390);
    const { result } = renderHook(() => useIsMobile());
    expect(result.current).toBe(false);
    act(() => media!.set(TOUCH_MEDIA_QUERY, true));
    expect(result.current).toBe(true);
  });
});
