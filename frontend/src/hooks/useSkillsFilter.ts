import { useMemo, useRef, useState } from 'react';
import type { CustomSkill } from '@/types/user.types';
import { fuzzySearch } from '@/utils/fuzzySearch';

export function useSkillsFilter(items: CustomSkill[], workspaceId: string | undefined) {
  const [searchQuery, setSearchQuery] = useState('');

  // Clear the search when the workspace changes so a query from one workspace
  // doesn't carry into the next.
  const prevWorkspaceRef = useRef(workspaceId);
  if (prevWorkspaceRef.current !== workspaceId) {
    prevWorkspaceRef.current = workspaceId;
    setSearchQuery('');
  }

  // Reuse the app-wide fuzzy search so skills match the same way as the command menu
  // and mention suggestions; limit stays at the full set since this isn't a dropdown.
  const filteredItems = useMemo(
    () => fuzzySearch(searchQuery, items, { keys: ['name', 'description'], limit: items.length }),
    [items, searchQuery],
  );

  return { searchQuery, setSearchQuery, filteredItems };
}
