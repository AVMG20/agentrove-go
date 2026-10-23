import { apiClient } from '@/lib/api';
import { ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { Artifact, ArtifactUpdateRequest, ArtifactView } from '@/types/artifact.types';

async function listArtifacts(): Promise<Artifact[]> {
  return serviceCall(async () => {
    const response = await apiClient.get<Artifact[]>('/artifacts');
    return ensureResponse(response, 'Failed to load artifacts');
  });
}

async function getArtifactView(artifactId: string): Promise<ArtifactView> {
  return serviceCall(async () => {
    const response = await apiClient.get<ArtifactView>(`/artifacts/${artifactId}/view`);
    return ensureResponse(response, 'Failed to load artifact');
  });
}

async function updateArtifact(artifactId: string, data: ArtifactUpdateRequest): Promise<Artifact> {
  return serviceCall(async () => {
    const response = await apiClient.patch<Artifact>(`/artifacts/${artifactId}`, data);
    return ensureResponse(response, 'Failed to update artifact');
  });
}

async function deleteArtifact(artifactId: string): Promise<void> {
  await serviceCall(async () => {
    await apiClient.delete(`/artifacts/${artifactId}`);
  });
}

// The sandboxed page itself, served by the API with its own CSP sandbox.
function getContentUrl(view: ArtifactView): string {
  return `${apiClient.getBaseUrl()}${view.content_path}`;
}

// The viewer page; the link agents and users share.
function getShareUrl(artifactId: string): string {
  return `${window.location.origin}/artifacts/${artifactId}`;
}

export const artifactService = {
  listArtifacts,
  getArtifactView,
  updateArtifact,
  deleteArtifact,
  getContentUrl,
  getShareUrl,
};
