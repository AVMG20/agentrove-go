import { useCallback, useMemo, useState } from 'react';
import { Box, Database, FolderGit2, HardDrive } from 'lucide-react';
import toast from 'react-hot-toast';
import { ListManagementTab } from '@/components/ui/ListManagementTab/ListManagementTab';
import { RenameModal } from '@/components/ui/RenameModal/RenameModal';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import {
  useDeleteWorkspaceMutation,
  useUpdateWorkspaceMutation,
  useWorkspaceDiskUsageQuery,
  useWorkspacesQuery,
} from '@/hooks/queries/useWorkspaceQueries';
import type { Workspace } from '@/types/workspace.types';
import { projectPath } from '@/utils/box';
import { formatRelativeTime } from '@/utils/date';
import { formatBytes } from '@/utils/format';
import styles from './WorkspacesSettingsTab.module.scss';

const SOURCE_ICONS = { git: FolderGit2, local: HardDrive, empty: Box } as const;

function describeSource(workspace: Workspace): string {
  if (workspace.source_type === 'git') return workspace.source_url ?? 'Git repository';
  return projectPath(workspace);
}

export function WorkspacesSettingsTab() {
  const { data: workspacesData } = useWorkspacesQuery();
  const { data: diskUsage } = useWorkspaceDiskUsageQuery();
  const diskUsageById = useMemo(
    () => new Map((diskUsage ?? []).map((usage) => [usage.workspace_id, usage])),
    [diskUsage],
  );

  const deleteWorkspace = useDeleteWorkspaceMutation();
  const updateWorkspace = useUpdateWorkspaceMutation();
  const [renaming, setRenaming] = useState<Workspace | null>(null);

  const workspaces = workspacesData?.items ?? null;

  const handleDelete = useCallback(
    async (index: number) => {
      const workspace = workspaces?.[index];
      if (!workspace) return;
      try {
        await deleteWorkspace.mutateAsync(workspace.id);
        toast.success('Project deleted');
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to delete project');
        throw error;
      }
    },
    [workspaces, deleteWorkspace],
  );

  const handleRename = useCallback(
    async (name: string) => {
      if (!renaming) return;
      try {
        await updateWorkspace.mutateAsync({ workspaceId: renaming.id, data: { name } });
        toast.success('Project renamed');
        setRenaming(null);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to rename project');
      }
    },
    [renaming, updateWorkspace],
  );

  return (
    <>
      <ListManagementTab<Workspace>
        title="Projects"
        description="Rename or delete projects, including ones without threads that don't show in the sidebar. Create new ones from the project picker in the composer."
        items={workspaces}
        emptyIcon={Box}
        emptyText="No projects yet"
        deleteConfirmTitle="Delete project"
        deleteConfirmMessage={(workspace) => {
          const threads = `${workspace.chat_count} ${workspace.chat_count === 1 ? 'thread' : 'threads'}`;
          return workspace.source_type === 'local'
            ? `Delete "${workspace.name}"? Its ${threads} are removed; the folder on this computer is kept. This can't be undone.`
            : `Delete "${workspace.name}"? Its ${threads} and files are removed. This can't be undone.`;
        }}
        getItemKey={(workspace) => workspace.id}
        onEdit={(index) => {
          const workspace = workspaces?.[index];
          if (workspace) setRenaming(workspace);
        }}
        onDelete={handleDelete}
        renderItem={(workspace) => {
          const SourceIcon =
            SOURCE_ICONS[workspace.source_type as keyof typeof SOURCE_ICONS] ?? Box;
          const usage = diskUsageById.get(workspace.id);
          return (
            <>
              <div className={styles['item-header']}>
                <SourceIcon className={styles['source-icon']} aria-hidden="true" />
                <h3 className={styles['item-title']}>{workspace.name}</h3>
              </div>
              <div className={styles['item-meta']}>
                <span className={styles.source}>{describeSource(workspace)}</span>
                <span>
                  {workspace.chat_count} {workspace.chat_count === 1 ? 'thread' : 'threads'}
                </span>
                {workspace.sandbox_provider === 'host' && <span>This computer</span>}
                {usage?.files_bytes != null && (
                  <FloatingTooltip
                    content="Project files, freed when the project is deleted"
                    className={styles.size}
                  >
                    <Database className={styles['size-icon']} aria-hidden="true" />
                    {formatBytes(usage.files_bytes)}
                  </FloatingTooltip>
                )}
                <span className={styles.muted}>
                  {workspace.last_chat_at
                    ? `Active ${formatRelativeTime(workspace.last_chat_at)}`
                    : `Created ${formatRelativeTime(workspace.created_at)}`}
                </span>
              </div>
            </>
          );
        }}
        logContext="WorkspacesSettingsTab"
      />
      <RenameModal
        isOpen={renaming !== null}
        onClose={() => setRenaming(null)}
        onSave={handleRename}
        currentTitle={renaming?.name ?? ''}
        isLoading={updateWorkspace.isPending}
        title="Rename project"
      />
    </>
  );
}
