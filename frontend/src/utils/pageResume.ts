export interface PageResumeInfo {
  // How long the page was hidden/offline; 0 when the away time is unknown.
  idleMs: number;
}

// Away at least this long → treat caches/feeds as stale (sleep, long tab switch).
export const PAGE_RESUME_IDLE_MS = 30_000;

export function isOffline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false;
}

// Fires when the page comes back: visible again, network back online, or restored
// from bfcache. Sockets may be half-open after sleep with no error ever firing.
export function subscribePageResume(callback: (info: PageResumeInfo) => void): () => void {
  if (typeof window === 'undefined' || typeof document === 'undefined') return () => {};

  let awaySince: number | null = document.visibilityState === 'hidden' ? Date.now() : null;

  const markAway = () => {
    // Keep the earliest mark so hidden-then-offline reports the full gap.
    awaySince ??= Date.now();
  };

  const emit = () => {
    const idleMs = awaySince === null ? 0 : Date.now() - awaySince;
    // Keep the mark until visible and online so the later resume reports the full gap.
    if (document.visibilityState !== 'hidden' && !isOffline()) awaySince = null;
    callback({ idleMs });
  };

  const onVisibilityChange = () => {
    if (document.visibilityState === 'hidden') markAway();
    else emit();
  };
  const onPageShow = (event: PageTransitionEvent) => {
    if (event.persisted) emit();
  };

  document.addEventListener('visibilitychange', onVisibilityChange);
  window.addEventListener('online', emit);
  window.addEventListener('offline', markAway);
  window.addEventListener('pagehide', markAway);
  window.addEventListener('pageshow', onPageShow);

  return () => {
    document.removeEventListener('visibilitychange', onVisibilityChange);
    window.removeEventListener('online', emit);
    window.removeEventListener('offline', markAway);
    window.removeEventListener('pagehide', markAway);
    window.removeEventListener('pageshow', onPageShow);
  };
}
