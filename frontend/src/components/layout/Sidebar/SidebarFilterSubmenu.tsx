import { type ReactNode } from 'react';
import { Check } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import {
  GROUP_BY_OPTIONS,
  STATUS_OPTIONS,
  type FilterCategory,
  type SidebarFilters,
  type SidebarStatusFilter,
} from '@/store/sidebarFilters';
import styles from './SidebarFilterSubmenu.module.scss';

interface WorkspaceOption {
  id: string;
  name: string;
}

function FilterOptionRow({
  selected,
  onSelect,
  children,
}: {
  selected: boolean;
  onSelect: () => void;
  children: ReactNode;
}) {
  return (
    <Button
      variant="unstyled"
      onClick={onSelect}
      className={clsx(styles['option-row'], selected && styles['option-row--selected'])}
    >
      <span className={styles['option-label']}>{children}</span>
      {selected && <Check className={styles['option-check']} />}
    </Button>
  );
}

interface SidebarFilterSubmenuProps {
  view: FilterCategory;
  filters: SidebarFilters;
  workspaceOptions: WorkspaceOption[];
  submenuRef: React.Ref<HTMLDivElement>;
  style: React.CSSProperties;
  onMouseEnter: () => void;
  onMouseLeave: () => void;
  onChange: (filters: SidebarFilters) => void;
  onToggleStatus: (status: SidebarStatusFilter) => void;
  onSelectAndClose: (patch: Partial<SidebarFilters>) => void;
}

export function SidebarFilterSubmenu({
  view,
  filters,
  workspaceOptions,
  submenuRef,
  style,
  onMouseEnter,
  onMouseLeave,
  onChange,
  onToggleStatus,
  onSelectAndClose,
}: SidebarFilterSubmenuProps) {
  return (
    <div
      ref={submenuRef}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      className={styles.panel}
      style={style}
    >
      {view === 'status' && (
        <>
          <FilterOptionRow
            selected={filters.statuses.length === 0}
            onSelect={() => onChange({ ...filters, statuses: [] })}
          >
            All
          </FilterOptionRow>
          {STATUS_OPTIONS.map(({ value, label }) => (
            <FilterOptionRow
              key={value}
              selected={filters.statuses.includes(value)}
              onSelect={() => onToggleStatus(value)}
            >
              {label}
            </FilterOptionRow>
          ))}
        </>
      )}

      {view === 'groupBy' &&
        GROUP_BY_OPTIONS.map(({ value, label }) => (
          <FilterOptionRow
            key={value}
            selected={filters.groupBy === value}
            onSelect={() => onSelectAndClose({ groupBy: value })}
          >
            {label}
          </FilterOptionRow>
        ))}

      {view === 'workspace' && (
        <>
          <FilterOptionRow
            selected={filters.workspaceId === null}
            onSelect={() => onSelectAndClose({ workspaceId: null })}
          >
            All
          </FilterOptionRow>
          {workspaceOptions.map((workspace) => (
            <FilterOptionRow
              key={workspace.id}
              selected={filters.workspaceId === workspace.id}
              onSelect={() => onSelectAndClose({ workspaceId: workspace.id })}
            >
              <span className={styles['option-name']}>{workspace.name}</span>
            </FilterOptionRow>
          ))}
        </>
      )}
    </div>
  );
}
