import type { Workspace } from '@/types/workspace.types';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { RenameModal } from '@/components/ui/RenameModal/RenameModal';
import { ChatDropdown } from './ChatDropdown';
import { WorkspaceContextMenu } from './WorkspaceContextMenu';
import type { SidebarChatActions } from './useSidebarChatActions';
import type { SidebarWorkspaceActions } from './useSidebarWorkspaceActions';
import type { FolderDropPlacement } from './sidebarGrouping';

interface SidebarOverlaysProps {
  chat: SidebarChatActions;
  workspace: SidebarWorkspaceActions;
  workspaces: Workspace[];
  // Folders on screen, in order, when the list is grouped by project: the
  // project menu offers Move up/down among them.
  folderKeys?: string[];
  onMoveFolder?: (key: string, targetKey: string, placement: FolderDropPlacement) => void;
}

export function SidebarOverlays({
  chat,
  workspace,
  workspaces,
  folderKeys = [],
  onMoveFolder,
}: SidebarOverlaysProps) {
  const folderMove = (workspaceId: string, step: -1 | 1) => {
    const index = folderKeys.indexOf(workspaceId);
    const target = index === -1 ? undefined : folderKeys[index + step];
    if (!onMoveFolder || !target) return undefined;
    return () => {
      workspace.setWorkspaceDropdown(null);
      onMoveFolder(workspaceId, target, step < 0 ? 'before' : 'after');
    };
  };

  const workspaceAppearance = (workspaceId: string) => {
    const ws = workspaces.find((w) => w.id === workspaceId);
    return { icon: ws?.icon ?? null, color: ws?.color ?? null };
  };

  return (
    <>
      {chat.dropdown && (
        <ChatDropdown
          ref={chat.dropdownRef}
          chat={chat.dropdown.chat}
          position={chat.dropdown.position}
          onRename={chat.handleRenameClick}
          onRegenerateTitle={chat.handleRegenerateTitle}
          onDelete={chat.handleDeleteChat}
          onTogglePin={chat.handleTogglePin}
          onArchive={chat.handleArchiveChat}
          onOpenInSplit={chat.dropdownShowSplit ? chat.handleDropdownOpenInSplit : undefined}
          splitDisabled={!chat.dropdownCanSplit}
          onClose={() => chat.setDropdown(null)}
        />
      )}

      {workspace.workspaceDropdown && (
        <WorkspaceContextMenu
          key={workspace.workspaceDropdown.workspaceId}
          ref={workspace.workspaceDropdownRef}
          position={workspace.workspaceDropdown.position}
          appearance={workspaceAppearance(workspace.workspaceDropdown.workspaceId)}
          name={workspaces.find((w) => w.id === workspace.workspaceDropdown!.workspaceId)?.name}
          onMoveUp={folderMove(workspace.workspaceDropdown.workspaceId, -1)}
          onMoveDown={folderMove(workspace.workspaceDropdown.workspaceId, 1)}
          onNewThread={() => {
            const { workspaceId } = workspace.workspaceDropdown!;
            workspace.setWorkspaceDropdown(null);
            workspace.handleNewWorkspaceThread(null, workspaceId);
          }}
          onRename={() => {
            const ws = workspaces.find((w) => w.id === workspace.workspaceDropdown!.workspaceId);
            if (ws) workspace.handleRenameWorkspace(ws);
          }}
          onChangeAppearance={(change) =>
            workspace.handleChangeWorkspaceAppearance(
              workspace.workspaceDropdown!.workspaceId,
              change,
            )
          }
          onDelete={() => workspace.handleDeleteWorkspace(workspace.workspaceDropdown!.workspaceId)}
          onClose={() => workspace.setWorkspaceDropdown(null)}
        />
      )}

      <ConfirmDialog
        isOpen={!!chat.chatToDelete}
        onClose={() => chat.setChatToDelete(null)}
        onConfirm={chat.confirmDeleteChat}
        title="Delete Chat"
        message="Are you sure you want to delete this chat? This action cannot be undone."
        confirmLabel="Delete"
        cancelLabel="Cancel"
      />

      <ConfirmDialog
        isOpen={!!workspace.workspaceToDelete}
        onClose={() => workspace.setWorkspaceToDelete(null)}
        onConfirm={workspace.confirmDeleteWorkspace}
        title="Delete project"
        message="Delete this project with all its threads and files? This can't be undone."
        confirmLabel="Delete"
        cancelLabel="Cancel"
      />

      <RenameModal
        isOpen={!!chat.chatToRename}
        onClose={() => chat.setChatToRename(null)}
        onSave={chat.handleSaveRename}
        currentTitle={chat.chatToRename?.title || ''}
        isLoading={chat.updateChat.isPending}
        onGenerateTitle={chat.handleGenerateChatTitle}
        isGenerating={chat.generateChatTitle.isPending}
      />

      <RenameModal
        isOpen={!!workspace.workspaceToRename}
        onClose={() => workspace.setWorkspaceToRename(null)}
        onSave={workspace.handleSaveWorkspaceRename}
        currentTitle={workspace.workspaceToRename?.name || ''}
        title="Rename project"
        isLoading={workspace.updateWorkspace.isPending}
      />
    </>
  );
}
