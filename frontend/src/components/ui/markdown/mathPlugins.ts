import { useEffect, useSyncExternalStore } from 'react';
import type remarkMathPlugin from 'remark-math';
import type rehypeKatexPlugin from 'rehype-katex';

export interface MathPlugins {
  remarkMath: typeof remarkMathPlugin;
  rehypeKatex: typeof rehypeKatexPlugin;
}

// KaTeX (+ its CSS and fonts) is ~300 kB and most chats never show math, so it's
// fetched on first use and shared by every MarkDown instance afterwards.
let loaded: MathPlugins | null = null;
let loading: Promise<void> | null = null;
const listeners = new Set<() => void>();

function loadMathPlugins(): Promise<void> {
  loading ??= Promise.all([
    import('remark-math'),
    import('rehype-katex'),
    import('katex/dist/katex.min.css'),
  ])
    .then(([remarkMath, rehypeKatex]) => {
      loaded = { remarkMath: remarkMath.default, rehypeKatex: rehypeKatex.default };
      listeners.forEach((listener) => listener());
    })
    .catch((error: unknown) => {
      // Leave math unrendered and allow a later message to retry the fetch.
      loading = null;
      console.error('Failed to load math rendering', error);
    });
  return loading;
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

const getSnapshot = (): MathPlugins | null => loaded;

// Returns the math plugins once loaded (null until then), kicking off the load the
// first time content needs them. Callers render without math until it resolves.
export function useMathPlugins(needsMath: boolean): MathPlugins | null {
  const plugins = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  useEffect(() => {
    if (needsMath && !loaded) void loadMathPlugins();
  }, [needsMath]);
  return needsMath ? plugins : null;
}
