import { hashKey } from '@tanstack/react-query';
import type { QueryClient, QueryFilters, QueryKey } from '@tanstack/react-query';

// The cached data an optimistic update is about to overwrite, to put back if
// the request fails.
export interface CacheSnapshot {
  restore: () => void;
}

// Cancels in-flight fetches of the matching queries, so a response from
// before the change can't land on top of it, then records their data.
export async function snapshotQueries(
  queryClient: QueryClient,
  filters: QueryFilters[],
): Promise<CacheSnapshot> {
  await Promise.all(filters.map((filter) => queryClient.cancelQueries(filter)));
  const entries = new Map<string, [QueryKey, unknown]>();
  for (const filter of filters) {
    for (const [key, data] of queryClient.getQueriesData(filter)) {
      entries.set(hashKey(key), [key, data]);
    }
  }
  return {
    restore: () => {
      for (const [key, data] of entries.values()) queryClient.setQueryData(key, data);
    },
  };
}
