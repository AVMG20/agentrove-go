import { useState, useRef, useCallback, type RefObject } from 'react';
import { useMountEffect } from '@/hooks/useMountEffect';
import type { NavigateFunction } from 'react-router-dom';
import type { Workspace } from '@/types/workspace.types';
import toast from 'react-hot-toast';
import {
  useDeleteWorkspaceMutation,
  useUpdateWorkspaceAppearanceMutation,
  useUpdateWorkspaceMutation,
} from '@/hooks/queries/useWorkspaceQueries';
import { useUIStore } from '@/store/uiStore';
import type { FolderAppearance } from './FolderAppearancePicker';
import { mutateWithToast, calculateDropdownPosition } from './sidebarHelpers';

interface UseSidebarWorkspaceActionsParams {
  selectedChatId: string | null;
  selectedChatWorkspaceId?: string | null;
  isMobile: boolean;
  navigate: NavigateFunction;
  scrollContainerRef: RefObject<HTMLDivElement | null>;
}

export function useSidebarWorkspaceActions({
  selectedChatId,
  selectedChatWorkspaceId,
  isMobile,
  navigate,
  scrollContainerRef,
}: UseSidebarWorkspaceActionsParams) {
  const [workspaceToDelete, setWorkspaceToDelete] = useState<string | null>(null);
  const [workspaceToRename, setWorkspaceToRename] = useState<Workspace | null>(null);
  const [workspaceDropdown, setWorkspaceDropdown] = useState<{
    workspaceId: string;
    position: { top: number; left: number };
  } | null>(null);

  const workspaceDropdownRef = useRef<HTMLDivElement>(null);
  const deleteWorkspace = useDeleteWorkspaceMutation();
  const updateWorkspace = useUpdateWorkspaceMutation();
  const updateWorkspaceAppearance = useUpdateWorkspaceAppearanceMutation();

  useMountEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        workspaceDropdownRef.current &&
        !workspaceDropdownRef.current.contains(event.target as Node) &&
        !(event.target as HTMLElement).closest('[data-ws-dropdown-trigger]')
      ) {
        setWorkspaceDropdown(null);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  });

  const wsDropdownStateRef = useRef(workspaceDropdown);
  wsDropdownStateRef.current = workspaceDropdown;

  useMountEffect(() => {
    const scrollContainer = scrollContainerRef.current;
    if (!scrollContainer) return;

    const handleScroll = () => {
      if (wsDropdownStateRef.current) setWorkspaceDropdown(null);
    };

    scrollContainer.addEventListener('scroll', handleScroll, { passive: true });
    return () => scrollContainer.removeEventListener('scroll', handleScroll);
  });

  const handleNewWorkspaceThread = useCallback(
    // null = a thread without a project.
    (e: React.MouseEvent | null, workspaceId: string | null) => {
      e?.stopPropagation();
      navigate('/', { state: { workspaceId } });
      if (isMobile) {
        useUIStore.getState().setSidebarOpen(false);
      }
    },
    [navigate, isMobile],
  );

  const handleWorkspaceContextMenu = useCallback(
    (e: React.MouseEvent<HTMLButtonElement>, workspaceId: string) => {
      e.stopPropagation();
      const rect = e.currentTarget.getBoundingClientRect();
      setWorkspaceDropdown((prev) => {
        if (prev?.workspaceId === workspaceId) return null;
        const position = calculateDropdownPosition(rect);
        return { workspaceId, position };
      });
    },
    [],
  );

  const handleRenameWorkspace = useCallback((workspace: Workspace) => {
    setWorkspaceToRename(workspace);
    setWorkspaceDropdown(null);
  }, []);

  const handleSaveWorkspaceRename = useCallback(
    async (newName: string) => {
      if (!workspaceToRename) return;
      try {
        await mutateWithToast(
          () =>
            updateWorkspace.mutateAsync({
              workspaceId: workspaceToRename.id,
              data: { name: newName },
            }),
          'Project renamed',
          'Failed to rename project',
        );
      } catch {
        // toast already shown by mutateWithToast
      } finally {
        setWorkspaceToRename(null);
      }
    },
    [workspaceToRename, updateWorkspace],
  );

  // Optimistic; a failure puts the folder back when the list refetches.
  const handleChangeWorkspaceAppearance = useCallback(
    (workspaceId: string, change: Partial<FolderAppearance>) => {
      updateWorkspaceAppearance.mutate(
        { workspaceId, data: change },
        { onError: () => toast.error('Failed to update the project folder') },
      );
    },
    [updateWorkspaceAppearance],
  );

  const handleDeleteWorkspace = useCallback((workspaceId: string) => {
    setWorkspaceToDelete(workspaceId);
    setWorkspaceDropdown(null);
  }, []);

  const confirmDeleteWorkspace = useCallback(async () => {
    if (!workspaceToDelete) return;
    const id = workspaceToDelete;
    try {
      await mutateWithToast(
        () => deleteWorkspace.mutateAsync(id),
        'Project deleted',
        'Failed to delete project',
      );
      if (selectedChatId && selectedChatWorkspaceId === id) {
        navigate('/');
      }
    } catch {
      // toast already shown by mutateWithToast
    } finally {
      setWorkspaceToDelete(null);
    }
  }, [workspaceToDelete, deleteWorkspace, selectedChatId, selectedChatWorkspaceId, navigate]);

  return {
    workspaceDropdown,
    setWorkspaceDropdown,
    workspaceDropdownRef,
    workspaceToDelete,
    setWorkspaceToDelete,
    workspaceToRename,
    setWorkspaceToRename,
    updateWorkspace,
    handleNewWorkspaceThread,
    handleWorkspaceContextMenu,
    handleRenameWorkspace,
    handleSaveWorkspaceRename,
    handleChangeWorkspaceAppearance,
    handleDeleteWorkspace,
    confirmDeleteWorkspace,
  };
}

export type SidebarWorkspaceActions = ReturnType<typeof useSidebarWorkspaceActions>;
