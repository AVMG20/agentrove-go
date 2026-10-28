import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { artifactService } from '@/services/artifactService';
import type { Artifact, ArtifactUpdateRequest, ArtifactView } from '@/types/artifact.types';
import { createMutation } from './createMutation';
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

export const useUpdateArtifactMutation = createMutation<
  Artifact,
  Error,
  { artifactId: string; data: ArtifactUpdateRequest }
>(
  ({ artifactId, data }) => artifactService.updateArtifact(artifactId, data),
  (queryClient) => queryClient.invalidateQueries({ queryKey: queryKeys.artifacts }),
);

export const useDeleteArtifactMutation = createMutation<void, Error, string>(
  (artifactId) => artifactService.deleteArtifact(artifactId),
  (queryClient) => queryClient.invalidateQueries({ queryKey: queryKeys.artifacts }),
);
