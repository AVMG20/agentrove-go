// Shared filter model for uiStore, Sidebar, and SidebarFilterMenu.

// unread is server-persisted; running/done/needs-you are session-only stream state.
export type SidebarStatusFilter = 'unread' | 'running' | 'done' | 'needs-you';
export type SidebarGroupBy = 'none' | 'date' | 'workspace' | 'status';
export type FilterCategory = 'status' | 'workspace' | 'groupBy';

export const STATUS_OPTIONS: { value: SidebarStatusFilter; label: string }[] = [
  { value: 'unread', label: 'Unread' },
  { value: 'running', label: 'Running' },
  { value: 'done', label: 'Done' },
  { value: 'needs-you', label: 'Needs you' },
];

export const GROUP_BY_OPTIONS: { value: SidebarGroupBy; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: 'date', label: 'Date' },
  { value: 'workspace', label: 'Project' },
  { value: 'status', label: 'Status' },
];

// Array (not Set) so the object survives JSON persistence; max 4 entries.
export interface SidebarFilters {
  statuses: SidebarStatusFilter[];
  workspaceId: string | null;
  // Presentation only — excluded from active count, kept on clear.
  groupBy: SidebarGroupBy;
}

// Immutable empty — filter changes always allocate a fresh object.
export const EMPTY_SIDEBAR_FILTERS: SidebarFilters = {
  statuses: [],
  workspaceId: null,
  // Workspace folders by default — the sidebar reads as projects, not a flat feed.
  groupBy: 'workspace',
};

// Resets filter dimensions but keeps presentation (groupBy).
export function clearSidebarFilters(filters: SidebarFilters): SidebarFilters {
  return { ...EMPTY_SIDEBAR_FILTERS, groupBy: filters.groupBy };
}

// A persisted project filter can outlive its project; drop it once the
// loaded project list doesn't have it (same object when nothing changes).
export function dropMissingWorkspaceFilter(
  filters: SidebarFilters,
  workspaceIds: string[],
): SidebarFilters {
  if (!filters.workspaceId || workspaceIds.includes(filters.workspaceId)) return filters;
  return { ...filters, workspaceId: null };
}

export function countActiveSidebarFilters(filters: SidebarFilters): number {
  return filters.statuses.length + (filters.workspaceId ? 1 : 0);
}
