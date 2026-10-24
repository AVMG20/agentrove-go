import { useCallback, useEffect, useRef } from 'react';

const OVERLAY_KEY = 'agentroveOverlay';

const overlayToken = (): unknown => {
  const state: unknown = window.history.state;
  return typeof state === 'object' && state !== null
    ? (state as Record<string, unknown>)[OVERLAY_KEY]
    : undefined;
};

// While `isOpen`, the browser/phone back action closes the overlay instead of
// leaving the page: opening adds a same-URL history entry (carrying the
// router's state, so routing sees no navigation) and back closes it. Close
// buttons should call the returned `requestClose`, which pops that entry so a
// later back press isn't a dead one.
export function useCloseOnBack(isOpen: boolean, onClose: () => void): () => void {
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  const tokenRef = useRef<string | null>(null);

  useEffect(() => {
    if (!isOpen) return;
    // Reuse our entry if it's already on top (React may run this effect twice).
    let token = overlayToken();
    if (typeof token !== 'string' || token !== tokenRef.current) {
      token = crypto.randomUUID();
      const base: unknown = window.history.state;
      window.history.pushState(
        { ...(typeof base === 'object' && base !== null ? base : {}), [OVERLAY_KEY]: token },
        '',
      );
    }
    tokenRef.current = token as string;
    const onPopState = () => {
      if (overlayToken() === tokenRef.current) return;
      tokenRef.current = null;
      onCloseRef.current();
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, [isOpen]);

  return useCallback(() => {
    if (tokenRef.current && overlayToken() === tokenRef.current) {
      // popstate then runs onClose.
      window.history.back();
      return;
    }
    tokenRef.current = null;
    onCloseRef.current();
  }, []);
}
