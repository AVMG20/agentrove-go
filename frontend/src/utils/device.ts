import { MOBILE_BREAKPOINT, PHONE_MAX_SHORT_SIDE } from '@/config/constants';

// The one definition of "touch" (finger as the main pointer: phones, tablets),
// shared by JS and the SCSS 'touch' query (_responsive.scss).
export const TOUCH_MEDIA_QUERY = '(pointer: coarse)';

export function isCoarsePointer(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.(TOUCH_MEDIA_QUERY).matches === true;
}

// A phone, by its screen rather than the viewport: the viewport shrinks for the
// on-screen keyboard in some browsers, and a short desktop or tablet window is
// not a phone.
export function isPhoneScreen(): boolean {
  if (typeof window === 'undefined' || !isCoarsePointer()) return false;
  const { width, height } = window.screen;
  return Math.min(width, height) < PHONE_MAX_SHORT_SIDE;
}

function isLandscape(): boolean {
  const type = window.screen.orientation?.type;
  if (type) return type.startsWith('landscape');
  return window.innerWidth > window.innerHeight;
}

export interface LayoutMode {
  // Phone layout: drawer sidebar, tab bar, sheets. Narrow windows and phones.
  mobile: boolean;
  // A phone on its side: height is what's scarce.
  phoneLandscape: boolean;
}

export function readLayoutMode(): LayoutMode {
  if (typeof window === 'undefined') return { mobile: false, phoneLandscape: false };
  const phone = isPhoneScreen();
  return {
    mobile: phone || window.innerWidth < MOBILE_BREAKPOINT,
    phoneLandscape: phone && isLandscape(),
  };
}

// CSS keys its 'mobile', 'desktop' and 'phone-landscape' modes off these
// attributes (_responsive.scss), so JS and CSS can't disagree.
export function applyLayoutAttributes(mode: LayoutMode = readLayoutMode()): void {
  const root = document.documentElement;
  root.toggleAttribute('data-mobile', mode.mobile);
  root.toggleAttribute('data-phone-landscape', mode.phoneLandscape);
}

// Calls back when the layout mode may have changed: resize, rotation, or the
// main pointer switching (a tablet's keyboard cover attached).
export function subscribeLayoutMode(onChange: () => void): () => void {
  const pointer = window.matchMedia?.(TOUCH_MEDIA_QUERY);
  const orientation = window.screen.orientation;
  window.addEventListener('resize', onChange, { passive: true });
  window.addEventListener('orientationchange', onChange);
  orientation?.addEventListener?.('change', onChange);
  pointer?.addEventListener?.('change', onChange);
  return () => {
    window.removeEventListener('resize', onChange);
    window.removeEventListener('orientationchange', onChange);
    orientation?.removeEventListener?.('change', onChange);
    pointer?.removeEventListener?.('change', onChange);
  };
}
