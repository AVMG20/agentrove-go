import { useState, useCallback, useMemo, type ReactNode, useId } from 'react';
import { FolderOpen, Search, ChevronDown, X } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import { BaseModal } from '@/components/ui/shared/BaseModal/BaseModal';
import { ModalHeader } from '@/components/ui/shared/ModalHeader/ModalHeader';
import type { Workspace } from '@/types/workspace.types';
import { NoProjectItem, WorkspaceItem } from './WorkspaceItem';
import styles from './WorkspacePicker.module.scss';

interface WorkspacePickerProps {
  workspaces: Workspace[];
  // null = no project; undefined = nothing picked yet.
  selectedWorkspaceId: string | null | undefined;
  onSelect: (id: string | null) => void;
  isLoading?: boolean;
  triggerDisabled?: boolean;
  // Creation flows; receives `close` so a new project can dismiss the modal.
  footer?: (close: () => void) => ReactNode;
}

export function WorkspacePicker({
  workspaces,
  selectedWorkspaceId,
  onSelect,
  isLoading = false,
  triggerDisabled = false,
  footer,
}: WorkspacePickerProps) {
  const titleId = useId();
  const [isOpen, setIsOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');

  const selectedWorkspace = workspaces.find((ws) => ws.id === selectedWorkspaceId);
  const hasSearchQuery = searchQuery.trim().length > 0;
  const visibleWorkspaces = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return workspaces;
    return workspaces.filter((ws) => ws.name.toLowerCase().includes(query));
  }, [workspaces, searchQuery]);

  const close = useCallback(() => {
    setIsOpen(false);
    setSearchQuery('');
  }, []);

  // Clicking the selected project again deselects it.
  const selectWorkspace = useCallback(
    (workspace: Workspace | null) => {
      onSelect(workspace && workspace.id !== selectedWorkspaceId ? workspace.id : null);
      close();
    },
    [onSelect, close, selectedWorkspaceId],
  );

  const label =
    selectedWorkspaceId === null
      ? 'No project'
      : (selectedWorkspace?.name ?? (isLoading ? 'Loading…' : 'Choose a project'));

  return (
    <>
      <Button
        variant="unstyled"
        type="button"
        disabled={triggerDisabled}
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        onClick={() => setIsOpen(true)}
        className={styles.trigger}
      >
        <FolderOpen className={styles['trigger-icon']} aria-hidden="true" />
        <span className={styles['trigger-label']}>{label}</span>
        <ChevronDown className={styles['trigger-icon']} aria-hidden="true" />
      </Button>

      <BaseModal isOpen={isOpen} onClose={close} size="md" ariaLabelledBy={titleId}>
        <ModalHeader title="Projects" titleId={titleId} onClose={close} />

        <div className={styles.body}>
          {workspaces.length > 0 && (
            <div className={styles['search-section']}>
              <div className={styles['search-field']}>
                <Search className={styles['search-icon']} aria-hidden="true" />
                <Input
                  variant="unstyled"
                  type="search"
                  name="workspace-search"
                  aria-label="Search projects"
                  autoComplete="off"
                  spellCheck={false}
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  placeholder="Search projects…"
                  className={styles['search-input']}
                />
                {searchQuery && (
                  <Button
                    variant="unstyled"
                    type="button"
                    aria-label="Clear project search"
                    onClick={() => setSearchQuery('')}
                    className={styles['clear-search']}
                  >
                    <X aria-hidden="true" />
                  </Button>
                )}
              </div>
            </div>
          )}

          {!hasSearchQuery && (
            <div className={styles['no-project']}>
              <NoProjectItem
                isSelected={selectedWorkspaceId === null}
                onSelect={() => selectWorkspace(null)}
              />
            </div>
          )}

          {workspaces.length > 0 && (
            <div className={styles.list}>
              {visibleWorkspaces.length > 0 && (
                <div className={styles['list-heading']}>
                  <span>{hasSearchQuery ? 'Search results' : 'Your projects'}</span>
                  <span>{visibleWorkspaces.length}</span>
                </div>
              )}
              {visibleWorkspaces.length === 0 ? (
                <div className={styles.empty}>
                  <FolderOpen className={styles['empty-icon']} aria-hidden="true" />
                  <p className={styles['empty-title']}>No matching projects</p>
                  <p className={styles['empty-description']}>Try a different name.</p>
                </div>
              ) : (
                <div className={styles['list-items']}>
                  {visibleWorkspaces.map((ws) => (
                    <WorkspaceItem
                      key={ws.id}
                      ws={ws}
                      isSelected={ws.id === selectedWorkspaceId}
                      isPickerOpen={isOpen}
                      onSelect={selectWorkspace}
                    />
                  ))}
                </div>
              )}
            </div>
          )}

          {footer && (
            <div className={styles.footer}>
              <div className={styles['footer-heading']}>New project</div>
              {footer(close)}
            </div>
          )}
        </div>
      </BaseModal>
    </>
  );
}
