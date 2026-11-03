import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import type { InfiniteData, QueryClient, QueryKey } from '@tanstack/react-query';
import { artifactService } from '@/services/artifactService';
import type { Artifact, ArtifactUpdateRequest, ArtifactView } from '@/types/artifact.types';
import { createMutation } from './createMutation';
import { snapshotQueries } from './optimistic';
import { queryKeys } from './queryKeys';

// Agents keep editing an artifact while it's open; poll so new versions show up.
const VIEW_REFRESH_MS = 15_000;

export const ARTIFACTS_PAGE_SIZE = 20;

// The artifacts a chat published (session panel).
export const useChatArtifactsQuery = (chatId: string) =>
  useQuery<Artifact[]>({
    queryKey: queryKeys.chatArtifacts(chatId),
    queryFn: () => artifactService.listArtifacts({ chat_id: chatId }),
    staleTime: 30_000,
  });

// Settings list: title search, a page at a time.
export const useArtifactPagesQuery = (search: string) =>
  useInfiniteQuery({
    queryKey: queryKeys.artifactPages(search),
    queryFn: ({ pageParam }) =>
      artifactService.listArtifacts({
        q: search || undefined,
        limit: ARTIFACTS_PAGE_SIZE,
        offset: pageParam,
      }),
    initialPageParam: 0,
    // A full page may have more behind it; a short one is the end.
    getNextPageParam: (lastPage, pages) =>
      lastPage.length < ARTIFACTS_PAGE_SIZE ? undefined : pages.length * ARTIFACTS_PAGE_SIZE,
    staleTime: 30_000,
  });

// poll=false keeps a hidden viewer's data without polling for new versions.
export const useArtifactViewQuery = (artifactId: string | undefined, poll = true) =>
  useQuery<ArtifactView>({
    queryKey: queryKeys.artifactView(artifactId),
    queryFn: () => artifactService.getArtifactView(artifactId!),
    enabled: !!artifactId,
    // A 404 (deleted, or private and not yours) won't fix itself on retry.
    retry: false,
    refetchInterval: poll ? VIEW_REFRESH_MS : false,
  });

// Shared by the optimistic edits below: only the last in flight refetches.
const artifactMutationKey = ['artifact-mutation'] as const;

// ['artifacts', id, 'view'], not a list (['artifacts', 'pages' | 'chat', …]).
const isArtifactViewKey = (key: QueryKey) =>
  key[2] === 'view' && key[1] !== 'pages' && key[1] !== 'chat';

// Rewrites the artifact lists (settings pages, each chat's list) and viewers.
async function applyArtifactsChange(
  queryClient: QueryClient,
  update: (artifacts: Artifact[]) => Artifact[],
  updateView?: (view: ArtifactView) => ArtifactView,
) {
  const snapshot = await snapshotQueries(queryClient, [{ queryKey: queryKeys.artifacts }]);
  queryClient.setQueriesData<InfiniteData<Artifact[]>>(
    { queryKey: [...queryKeys.artifacts, 'pages'] },
    (old) => old && { ...old, pages: old.pages.map(update) },
  );
  queryClient.setQueriesData<Artifact[]>(
    { queryKey: [...queryKeys.artifacts, 'chat'] },
    (old) => old && update(old),
  );
  if (updateView) {
    queryClient.setQueriesData<ArtifactView>(
      { queryKey: queryKeys.artifacts, predicate: (q) => isArtifactViewKey(q.queryKey) },
      (old) => old && updateView(old),
    );
  }
  return snapshot;
}

function patchArtifact<T extends Artifact>(artifact: T, data: ArtifactUpdateRequest): T {
  const { pinned, ...fields } = data;
  return {
    ...artifact,
    ...fields,
    ...(pinned !== undefined && { pinned_at: pinned ? new Date().toISOString() : null }),
  };
}

function settleArtifacts(queryClient: QueryClient) {
  void queryClient.invalidateQueries({ queryKey: queryKeys.artifacts });
}

// Optimistic rename, visibility, and pin; the refetch reorders pinned ones.
export const useUpdateArtifactMutation = createMutation<
  Artifact,
  Error,
  { artifactId: string; data: ArtifactUpdateRequest }
>(
  ({ artifactId, data }) => artifactService.updateArtifact(artifactId, data),
  () => undefined,
  {
    mutationKey: artifactMutationKey,
    scope: 'artifact-update',
    apply: (queryClient, { artifactId, data }) =>
      applyArtifactsChange(
        queryClient,
        (artifacts) => artifacts.map((a) => (a.id === artifactId ? patchArtifact(a, data) : a)),
        (view) => (view.id === artifactId ? patchArtifact(view, data) : view),
      ),
    settle: settleArtifacts,
  },
);

export const useDeleteArtifactMutation = createMutation<void, Error, string>(
  (artifactId) => artifactService.deleteArtifact(artifactId),
  () => undefined,
  {
    mutationKey: artifactMutationKey,
    apply: (queryClient, artifactId) =>
      applyArtifactsChange(queryClient, (artifacts) =>
        artifacts.filter((a) => a.id !== artifactId),
      ),
    settle: settleArtifacts,
  },
);
