import { useState } from 'react';
import { MOBILE_BREAKPOINT, PHONE_LANDSCAPE_MAX_HEIGHT } from '@/config/constants';
import { useMountEffect } from '@/hooks/useMountEffect';

// Narrow, or a phone on its side (wide but short, touch): both get the phone
// layout. Same query as the SCSS 'mobile' breakpoint (_responsive.scss).
export const MOBILE_MEDIA_QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px), (max-height: ${
  PHONE_LANDSCAPE_MAX_HEIGHT - 1
}px) and (pointer: coarse)`;

// Point-in-time read for non-render code paths; useIsMobile is the reactive form.
export const isMobileViewport = (): boolean => {
  if (typeof window === 'undefined') return false;
  if (typeof window.matchMedia !== 'function') return window.innerWidth < MOBILE_BREAKPOINT;
  return window.matchMedia(MOBILE_MEDIA_QUERY).matches;
};

export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(isMobileViewport());

  useMountEffect(() => {
    const handleResize = () => {
      setIsMobile(isMobileViewport());
    };

    window.addEventListener('resize', handleResize, { passive: true });
    // Re-check after attaching: a resize between the initial render and this
    // effect (e.g. the window sizing during startup) is otherwise missed
    // forever, leaving the sidebar stuck in mobile overlay mode on desktop.
    handleResize();
    return () => window.removeEventListener('resize', handleResize);
  });

  return isMobile;
}
