// Test doubles for the device APIs jsdom lacks or fixes: matchMedia, the screen
// size and orientation, and the viewport size.
import { vi } from 'vitest';

type ChangeListener = (event: { matches: boolean; media: string }) => void;

export interface MatchMediaStub {
  // Flip a query and notify its 'change' listeners.
  set(query: string, matches: boolean): void;
  restore(): void;
}

export function stubMatchMedia(initial: Record<string, boolean> = {}): MatchMediaStub {
  const state = new Map(Object.entries(initial));
  const listeners = new Map<string, Set<ChangeListener>>();
  const had = 'matchMedia' in window;
  const original = window.matchMedia;

  window.matchMedia = ((query: string) => {
    const forQuery = listeners.get(query) ?? new Set<ChangeListener>();
    listeners.set(query, forQuery);
    return {
      media: query,
      get matches() {
        return state.get(query) ?? false;
      },
      onchange: null,
      addEventListener: (_type: string, fn: ChangeListener) => forQuery.add(fn),
      removeEventListener: (_type: string, fn: ChangeListener) => forQuery.delete(fn),
      addListener: (fn: ChangeListener) => forQuery.add(fn),
      removeListener: (fn: ChangeListener) => forQuery.delete(fn),
      dispatchEvent: () => true,
    };
  }) as unknown as typeof window.matchMedia;

  return {
    set(query, matches) {
      state.set(query, matches);
      listeners.get(query)?.forEach((fn) => fn({ matches, media: query }));
    },
    restore() {
      if (had) window.matchMedia = original;
      else delete (window as { matchMedia?: unknown }).matchMedia;
    },
  };
}

// A finger as the main pointer (TOUCH_MEDIA_QUERY).
export const stubTouch = (touch = true) => stubMatchMedia({ '(pointer: coarse)': touch });

export interface ScreenStub {
  setOrientation(type: 'portrait-primary' | 'landscape-primary'): void;
  restore(): void;
}

// Screen size in CSS px, and optionally screen.orientation (left out, the code
// falls back to comparing the viewport's sides, as on older iOS).
export function stubScreen(
  width: number,
  height: number,
  orientation?: 'portrait-primary' | 'landscape-primary',
): ScreenStub {
  const widthSpy = vi.spyOn(window.screen, 'width', 'get').mockReturnValue(width);
  const heightSpy = vi.spyOn(window.screen, 'height', 'get').mockReturnValue(height);
  const target = new EventTarget();
  let type = orientation;
  if (orientation) {
    Object.defineProperty(target, 'type', { get: () => type });
    Object.defineProperty(window.screen, 'orientation', { configurable: true, value: target });
  }
  return {
    setOrientation(next) {
      type = next;
      target.dispatchEvent(new Event('change'));
    },
    restore() {
      widthSpy.mockRestore();
      heightSpy.mockRestore();
      delete (window.screen as { orientation?: unknown }).orientation;
    },
  };
}

export function setViewport(width: number, height: number) {
  window.innerWidth = width;
  window.innerHeight = height;
}
