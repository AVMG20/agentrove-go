import { useMutation, useQueryClient } from '@tanstack/react-query';
import type { MutationKey, QueryClient, UseMutationOptions } from '@tanstack/react-query';
import type { CacheSnapshot } from './optimistic';

export interface CacheUpdateMeta {
  // No other mutation under the same optimistic key is still in flight. When
  // false, a later optimistic change sits on top and this response is stale.
  isLast: boolean;
}

export interface OptimisticConfig<TError, TVariables> {
  // Mutations under this key (prefix-matched) share one settle: only the last
  // to finish runs it, so an earlier refetch can't undo a later change.
  mutationKey: MutationKey;
  // Sends the requests one at a time in call order (e.g. archive, then Undo).
  scope?: string;
  // Updates the cache before the request; the snapshot is restored if it fails.
  apply: (queryClient: QueryClient, variables: TVariables) => Promise<CacheSnapshot | undefined>;
  // After the last in-flight mutation under mutationKey ends, either way.
  settle?: (
    queryClient: QueryClient,
    variables: TVariables,
    error: TError | null,
  ) => void | Promise<void>;
}

type MutationHookOptions<TData, TError, TVariables> = Omit<
  UseMutationOptions<TData, TError, TVariables, CacheSnapshot | undefined>,
  'mutationFn' | 'mutationKey' | 'onMutate' | 'scope'
>;

export function createMutation<TData, TError, TVariables>(
  mutationFn: (variables: TVariables) => Promise<TData>,
  onCacheUpdate: (
    queryClient: QueryClient,
    data: TData,
    variables: TVariables,
    meta: CacheUpdateMeta,
  ) => void | Promise<void>,
  optimistic?: OptimisticConfig<TError, TVariables>,
) {
  return function useMutationHook(options?: MutationHookOptions<TData, TError, TVariables>) {
    const queryClient = useQueryClient();
    const { onSuccess, onError, onSettled, ...restOptions } = options ?? {};
    // Still counts itself: a mutation is pending until its callbacks finish.
    const isLast = () =>
      !optimistic || queryClient.isMutating({ mutationKey: optimistic.mutationKey }) <= 1;

    return useMutation<TData, TError, TVariables, CacheSnapshot | undefined>({
      ...restOptions,
      mutationFn,
      ...(optimistic && {
        mutationKey: optimistic.mutationKey,
        scope: optimistic.scope ? { id: optimistic.scope } : undefined,
        onMutate: async (variables) => {
          try {
            return await optimistic.apply(queryClient, variables);
          } catch (e) {
            console.error('Optimistic update failed:', e);
            return undefined;
          }
        },
      }),
      onSuccess: async (data, variables, snapshot, context) => {
        try {
          await onCacheUpdate(queryClient, data, variables, { isLast: isLast() });
        } catch (e) {
          console.error('Cache update failed:', e);
        }
        if (onSuccess) {
          await onSuccess(data, variables, snapshot, context);
        }
      },
      onError: async (error, variables, snapshot, context) => {
        snapshot?.restore();
        if (onError) {
          await onError(error, variables, snapshot, context);
        }
      },
      onSettled: async (data, error, variables, snapshot, context) => {
        if (optimistic?.settle && isLast()) {
          try {
            await optimistic.settle(queryClient, variables, error);
          } catch (e) {
            console.error('Cache settle failed:', e);
          }
        }
        if (onSettled) {
          await onSettled(data, error, variables, snapshot, context);
        }
      },
    });
  };
}
