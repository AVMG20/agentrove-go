import { apiClient } from '@/lib/api';
import { buildQueryString, ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { Artifact, ArtifactUpdateRequest, ArtifactView } from '@/types/artifact.types';

export interface ArtifactListParams {
  q?: string;
  chat_id?: string;
  limit?: number;
  offset?: number;
}

// Newest first; without limit the whole list.
async function listArtifacts(params?: ArtifactListParams): Promise<Artifact[]> {
  return serviceCall(async () => {
    const response = await apiClient.get<Artifact[]>(
      `/artifacts${buildQueryString({ ...params })}`,
    );
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

// Script errors the owner's viewer caught on a version; the agent reads them.
async function reportErrors(
  artifactId: string,
  version: number,
  messages: string[],
): Promise<void> {
  await serviceCall(async () => {
    await apiClient.post(`/artifacts/${artifactId}/errors`, { version, messages });
  });
}

// Kit.store state: JSON values by key, owner-only.
async function getState(artifactId: string): Promise<Record<string, unknown>> {
  return serviceCall(async () => {
    const response = await apiClient.get<{ state: Record<string, unknown> }>(
      `/artifacts/${artifactId}/state`,
    );
    return ensureResponse(response, 'Failed to load artifact state').state;
  });
}

async function setState(artifactId: string, key: string, value: unknown): Promise<void> {
  await serviceCall(async () => {
    const path = `/artifacts/${artifactId}/state/${encodeURIComponent(key)}`;
    if (value === undefined || value === null) await apiClient.delete(path);
    else await apiClient.put(path, { value });
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
  reportErrors,
  getState,
  setState,
  getContentUrl,
  getShareUrl,
};
