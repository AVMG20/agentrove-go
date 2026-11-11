import { useEffect, useMemo, useRef } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import type { QueryClient, UseQueryOptions } from '@tanstack/react-query';
import { workspaceService } from '@/services/workspaceService';
import type {
  BoxInfo,
  PruneBoxResult,
  Workspace,
  WorkspaceResources,
  CreateWorkspaceRequest,
  UpdateWorkspaceRequest,
} from '@/types/workspace.types';
import type { PaginatedResponse } from '@/types/api.types';
import type { Chat } from '@/types/chat.types';
import { useStreamStore } from '@/store/streamStore';
import { createMutation } from './createMutation';
import { snapshotQueries } from './optimistic';
import { queryKeys } from './queryKeys';

export const useWorkspaceResourcesQuery = (
  workspaceId: string | undefined,
  options?: Partial<UseQueryOptions<WorkspaceResources>>,
) => {
  return useQuery({
    queryKey: queryKeys.workspaceResources(workspaceId),
    queryFn: () => workspaceService.getWorkspaceResources(workspaceId!),
    enabled: !!workspaceId,
    staleTime: 30_000,
    ...options,
  });
};

export const useWorkspacesQuery = (
  options?: Partial<UseQueryOptions<PaginatedResponse<Workspace>>>,
) => {
  return useQuery({
    queryKey: queryKeys.workspaces,
    queryFn: () => workspaceService.listWorkspaces(),
    ...options,
  });
};

// The hidden workspace of threads without a project. Its id never changes.
const scratchWorkspaceQuery = () => ({
  queryKey: queryKeys.scratchWorkspace,
  queryFn: () => workspaceService.getScratchWorkspace(),
  staleTime: Infinity,
});

export const useScratchWorkspaceQuery = (enabled: boolean) => {
  return useQuery({ ...scratchWorkspaceQuery(), enabled });
};

// At send time, which may come before the query above has loaded.
export const fetchScratchWorkspace = (queryClient: QueryClient): Promise<Workspace> =>
  queryClient.ensureQueryData(scratchWorkspaceQuery());

// Sizing walks every workspace on disk, so the backend caches it for minutes too.
export const useWorkspaceDiskUsageQuery = () => {
  return useQuery({
    queryKey: queryKeys.workspaceDiskUsage,
    queryFn: () => workspaceService.getDiskUsage(),
    staleTime: 5 * 60 * 1000,
  });
};

const BOX_POLL_MS = 45_000;

// The user's box. Polled lightly while the tab is visible (interval refetches
// pause in the background) and refreshed when a turn starts or ends, which is
// when it wakes up or its idle clock restarts.
export const useBoxQuery = () => {
  const queryClient = useQueryClient();
  const working = useStreamStore((state) => state.activeStreamMetadata.length > 0);
  const wasWorking = useRef(working);
  useEffect(() => {
    if (wasWorking.current === working) return;
    wasWorking.current = working;
    // A beat later, so a waking box reports as running.
    const timer = setTimeout(
      () => void queryClient.invalidateQueries({ queryKey: queryKeys.box }),
      2_000,
    );
    return () => clearTimeout(timer);
  }, [working, queryClient]);

  return useQuery<BoxInfo>({
    queryKey: queryKeys.box,
    queryFn: () => workspaceService.getBox(),
    refetchInterval: (q) => (q.state.data?.available === false ? false : BOX_POLL_MS),
    staleTime: 15_000,
    retry: false,
  });
};

export const useStopBoxMutation = createMutation<BoxInfo, Error, { reset?: boolean }>(
  ({ reset }) => workspaceService.stopBox(reset),
  (queryClient, box) => {
    queryClient.setQueryData(queryKeys.box, box);
  },
);

export const usePruneBoxMutation = createMutation<PruneBoxResult, Error, void>(
  () => workspaceService.pruneBox(),
  (queryClient, result) => {
    queryClient.setQueryData(queryKeys.box, result.box);
    void queryClient.invalidateQueries({ queryKey: queryKeys.workspaceDiskUsage });
  },
);

// Stable [] when data is missing — avoids new-ref churn for downstream deps.
export const useWorkspacesList = (
  options?: Partial<UseQueryOptions<PaginatedResponse<Workspace>>>,
): Workspace[] => {
  const { data } = useWorkspacesQuery(options);
  return useMemo(() => data?.items ?? [], [data?.items]);
};

export const useCreateWorkspaceMutation = createMutation<Workspace, Error, CreateWorkspaceRequest>(
  (data) => workspaceService.createWorkspace(data),
  (queryClient, workspace) => {
    // In the list right away, so pickers that drop a selection missing from
    // the list keep the new project selected while the refetch runs.
    queryClient.setQueryData<PaginatedResponse<Workspace>>(queryKeys.workspaces, (old) =>
      old
        ? {
            ...old,
            items: [{ ...workspace, chat_count: 0, last_chat_at: null }, ...old.items],
            total: old.total + 1,
          }
        : old,
    );
    void queryClient.invalidateQueries({ queryKey: queryKeys.workspaces });
  },
);

// Under the workspaces key so useChatEvents holds off refetching while it runs.
const workspaceUpdateMutationKey = [...queryKeys.workspaces, 'update'];

// A rename or a folder icon/color. Optimistic, so the folder repaints at once;
// saves go out in order and only the last in flight settles the list.
export const useUpdateWorkspaceMutation = createMutation<
  Workspace,
  Error,
  { workspaceId: string; data: UpdateWorkspaceRequest }
>(
  ({ workspaceId, data }) => workspaceService.updateWorkspace(workspaceId, data),
  () => undefined,
  {
    mutationKey: workspaceUpdateMutationKey,
    scope: 'workspace-update',
    apply: async (queryClient, { workspaceId, data }) => {
      const snapshot = await snapshotQueries(queryClient, [
        { queryKey: queryKeys.workspaces, exact: true },
      ]);
      queryClient.setQueryData<PaginatedResponse<Workspace>>(queryKeys.workspaces, (old) =>
        old
          ? {
              ...old,
              items: old.items.map((w) => (w.id === workspaceId ? { ...w, ...data } : w)),
            }
          : old,
      );
      return snapshot;
    },
    settle: (queryClient) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.workspaces, exact: true });
      // Search results embed workspace_name, so a rename leaves a stale label
      // visible until the search query naturally refetches.
      void queryClient.invalidateQueries({ queryKey: queryKeys.chatsSearchAll });
    },
  },
);

// Same mutation; the folder appearance picker's name for it.
export const useUpdateWorkspaceAppearanceMutation = useUpdateWorkspaceMutation;

export const useDeleteWorkspaceMutation = createMutation<void, Error, string>(
  (workspaceId) => workspaceService.deleteWorkspace(workspaceId),
  async (queryClient, _data, workspaceId) => {
    const allCachedChats = queryClient.getQueriesData<Chat>({ queryKey: ['chat'] });
    for (const [, chat] of allCachedChats) {
      if (chat?.workspace_id === workspaceId) {
        queryClient.removeQueries({ queryKey: queryKeys.chat(chat.id) });
        queryClient.removeQueries({ queryKey: queryKeys.messages(chat.id) });
        queryClient.removeQueries({ queryKey: queryKeys.contextUsage(chat.id) });
      }
    }

    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.workspaces }),
      queryClient.invalidateQueries({ queryKey: [queryKeys.chats] }),
    ]);
  },
);
