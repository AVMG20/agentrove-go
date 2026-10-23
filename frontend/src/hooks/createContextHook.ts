import { use } from 'react';
import type { Context } from 'react';

export function createContextHook<T>(
  context: Context<T | null>,
  hookName: string,
  providerName: string,
): () => T {
  return function useContextHook(): T {
    const value = use(context);
    if (!value) {
      throw new Error(`${hookName} must be used within a ${providerName}`);
    }
    return value;
  };
}
