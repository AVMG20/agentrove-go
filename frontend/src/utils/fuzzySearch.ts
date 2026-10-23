import fuzzysort from 'fuzzysort';

interface FuzzySearchOptions {
  keys?: string[];
  limit: number;
  threshold?: number;
}

export function fuzzySearch<T>(
  query: string | undefined,
  items: T[],
  options: FuzzySearchOptions,
): T[] {
  if (items.length === 0) return [];
  if (!query || !query.trim()) return items.slice(0, options.limit);

  const threshold = options.threshold ?? -10000;

  if (!options.keys) {
    const results = fuzzysort.go(query, items as unknown as string[], {
      limit: options.limit,
      threshold,
    });
    return results.map((r) => r.target as unknown as T);
  }

  const results = fuzzysort.go(query, items, {
    keys: options.keys,
    limit: options.limit,
    threshold,
  });

  return results.map((result) => result.obj);
}
