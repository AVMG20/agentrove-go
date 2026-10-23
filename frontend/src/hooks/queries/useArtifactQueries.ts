import { useQuery } from '@tanstack/react-query';
import { artifactService } from '@/services/artifactService';
import type { Artifact, ArtifactUpdateRequest, ArtifactView } from '@/types/artifact.types';
import { createMutation } from './createMutation';
import { queryKeys } from './queryKeys';

// Agents keep editing an artifact while it's open; poll so new versions show up.
const VIEW_REFRESH_MS = 15_000;

export const useArtifactsQuery = () =>
  useQuery<Artifact[]>({
    queryKey: queryKeys.artifacts,
    queryFn: () => artifactService.listArtifacts(),
    staleTime: 30_000,
  });

export const useArtifactViewQuery = (artifactId: string | undefined) =>
  useQuery<ArtifactView>({
    queryKey: queryKeys.artifactView(artifactId),
    queryFn: () => artifactService.getArtifactView(artifactId!),
    enabled: !!artifactId,
    // A 404 (deleted, or private and not yours) won't fix itself on retry.
    retry: false,
    refetchInterval: VIEW_REFRESH_MS,
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
