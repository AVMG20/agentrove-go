import { useEffect, type DependencyList } from 'react';

export function useAsyncEffect(
  effect: (cancelled: () => boolean) => Promise<void>,
  deps: DependencyList,
): void {
  useEffect(() => {
    let cancelled = false;

    void effect(() => cancelled).catch((err) => {
      if (!cancelled) console.error('useAsyncEffect error:', err);
    });

    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
}
