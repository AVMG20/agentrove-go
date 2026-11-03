// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { act, cleanup, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { createMutation } from './createMutation';
import { snapshotQueries } from './optimistic';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const flush = () => act(async () => {});

afterEach(cleanup);

describe('snapshotQueries', () => {
  it('restores every matching query to its data at snapshot time', async () => {
    const client = new QueryClient();
    client.setQueryData(['items', 'a'], [1]);
    client.setQueryData(['items', 'b'], [2]);
    client.setQueryData(['other'], 'untouched');

    const snapshot = await snapshotQueries(client, [{ queryKey: ['items'] }]);
    client.setQueryData(['items', 'a'], [1, 10]);
    client.setQueryData(['items', 'b'], []);
    client.setQueryData(['other'], 'changed');
    snapshot.restore();

    expect(client.getQueryData(['items', 'a'])).toEqual([1]);
    expect(client.getQueryData(['items', 'b'])).toEqual([2]);
    expect(client.getQueryData(['other'])).toBe('changed');
  });

  it('cancels an in-flight fetch so its stale result does not land', async () => {
    const client = new QueryClient();
    client.setQueryData(['items'], ['old']);
    const response = deferred<string[]>();
    void client.fetchQuery({ queryKey: ['items'], queryFn: () => response.promise });

    await snapshotQueries(client, [{ queryKey: ['items'] }]);
    client.setQueryData(['items'], ['optimistic']);
    response.resolve(['stale']);
    await response.promise;

    expect(client.getQueryData(['items'])).toEqual(['optimistic']);
  });
});

describe('createMutation with optimistic config', () => {
  function setup() {
    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
    client.setQueryData(['count'], 1);
    const requests: ReturnType<typeof deferred<number>>[] = [];
    const onCacheUpdate = vi.fn();
    const settle = vi.fn();
    const useIncrement = createMutation<number, Error, number>(
      () => {
        const request = deferred<number>();
        requests.push(request);
        return request.promise;
      },
      onCacheUpdate,
      {
        mutationKey: ['count-mutation'],
        apply: async (queryClient, by) => {
          const snapshot = await snapshotQueries(queryClient, [{ queryKey: ['count'] }]);
          queryClient.setQueryData<number>(['count'], (n) => (n ?? 0) + by);
          return snapshot;
        },
        settle,
      },
    );
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    );
    const { result } = renderHook(() => useIncrement(), { wrapper });
    return { client, requests, onCacheUpdate, settle, result };
  }

  it('applies before the request and restores the snapshot when it fails', async () => {
    const { client, requests, settle, result } = setup();

    let outcome: Promise<unknown> | undefined;
    act(() => {
      outcome = result.current.mutateAsync(5).catch((e: unknown) => e);
    });
    await flush();
    expect(client.getQueryData(['count'])).toBe(6);

    requests[0].reject(new Error('nope'));
    expect(await outcome).toBeInstanceOf(Error);
    expect(client.getQueryData(['count'])).toBe(1);
    expect(settle).toHaveBeenCalledWith(client, 5, expect.any(Error));
  });

  it('settles once, after the last in-flight mutation, and flags stale responses', async () => {
    const { client, requests, onCacheUpdate, settle, result } = setup();

    let first: Promise<unknown> | undefined;
    let second: Promise<unknown> | undefined;
    act(() => {
      first = result.current.mutateAsync(1);
      second = result.current.mutateAsync(2);
    });
    await flush();
    expect(client.getQueryData(['count'])).toBe(4);

    requests[0].resolve(2);
    await first;
    expect(onCacheUpdate).toHaveBeenLastCalledWith(client, 2, 1, { isLast: false });
    expect(settle).not.toHaveBeenCalled();

    requests[1].resolve(4);
    await second;
    expect(onCacheUpdate).toHaveBeenLastCalledWith(client, 4, 2, { isLast: true });
    expect(settle).toHaveBeenCalledTimes(1);
    expect(settle).toHaveBeenCalledWith(client, 2, null);
  });
});
