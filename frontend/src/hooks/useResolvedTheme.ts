import { useSyncExternalStore } from 'react';
import { useUIStore } from '@/store/uiStore';
import type { ResolvedTheme } from '@/types/ui.types';

function getMediaQuery(): MediaQueryList | null {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return null;
  }
  return window.matchMedia('(prefers-color-scheme: dark)');
}

function subscribe(callback: () => void): () => void {
  const mediaQuery = getMediaQuery();
  if (!mediaQuery) {
    return () => {};
  }

  if (typeof mediaQuery.addEventListener === 'function') {
    mediaQuery.addEventListener('change', callback);
    return () => mediaQuery.removeEventListener('change', callback);
  }

  mediaQuery.addListener(callback);
  return () => mediaQuery.removeListener(callback);
}

function getSnapshot(): boolean {
  return getMediaQuery()?.matches ?? true;
}

function getServerSnapshot(): boolean {
  return true;
}

export function useResolvedTheme(): ResolvedTheme {
  const theme = useUIStore((state) => state.theme);
  const prefersDark = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  if (theme === 'system') {
    return prefersDark ? 'dark' : 'light';
  }
  return theme;
}
