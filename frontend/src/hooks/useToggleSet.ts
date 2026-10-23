import { useState, useCallback, type Dispatch, type SetStateAction } from 'react';

export function useToggleSet<T>(
  initialValue?: Iterable<T>,
): [Set<T>, (item: T) => void, Dispatch<SetStateAction<Set<T>>>] {
  const [set, setSet] = useState<Set<T>>(() => new Set(initialValue));

  const toggle = useCallback((item: T) => {
    setSet((prev) => {
      const next = new Set(prev);
      if (next.has(item)) next.delete(item);
      else next.add(item);
      return next;
    });
  }, []);

  return [set, toggle, setSet];
}
