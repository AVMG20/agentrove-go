import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import { WorkspacePicker } from './WorkspacePicker';
import { NewProject } from './NewProject';

interface WorkspaceSelectorProps {
  selectedWorkspaceId: string | null;
  onWorkspaceChange: (workspaceId: string | null) => void;
}

export function WorkspaceSelector({
  selectedWorkspaceId,
  onWorkspaceChange,
}: WorkspaceSelectorProps) {
  const workspaces = useWorkspacesList();

  return (
    <WorkspacePicker
      workspaces={workspaces}
      selectedWorkspaceId={selectedWorkspaceId}
      onSelect={onWorkspaceChange}
      footer={(close) => (
        <NewProject
          onCreated={(id) => {
            onWorkspaceChange(id);
            close();
          }}
          onNavigate={close}
        />
      )}
    />
  );
}
