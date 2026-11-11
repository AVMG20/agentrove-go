import { apiClient } from '@/lib/api';
import { ensureResponse, serviceCall, buildQueryString } from '@/services/base/BaseService';
import type {
  BoxInfo,
  PruneBoxResult,
  Workspace,
  WorkspaceDiskUsage,
  WorkspaceResources,
  CreateWorkspaceRequest,
  UpdateWorkspaceRequest,
} from '@/types/workspace.types';
import type { PaginationParams, PaginatedResponse } from '@/types/api.types';

async function listWorkspaces(
  pagination?: PaginationParams,
): Promise<PaginatedResponse<Workspace>> {
  return serviceCall(async () => {
    const queryString = buildQueryString(pagination ? { ...pagination } : undefined);
    const response = await apiClient.get<PaginatedResponse<Workspace>>(`/workspaces${queryString}`);
    return ensureResponse(response, 'Failed to fetch projects');
  });
}

async function createWorkspace(data: CreateWorkspaceRequest): Promise<Workspace> {
  return serviceCall(async () => {
    const response = await apiClient.post<Workspace>('/workspaces', data);
    return ensureResponse(response, 'Failed to create project');
  });
}

async function updateWorkspace(
  workspaceId: string,
  data: UpdateWorkspaceRequest,
): Promise<Workspace> {
  return serviceCall(async () => {
    const response = await apiClient.patch<Workspace>(`/workspaces/${workspaceId}`, data);
    return ensureResponse(response, 'Failed to update project');
  });
}

async function deleteWorkspace(workspaceId: string): Promise<void> {
  await serviceCall(async () => {
    await apiClient.delete(`/workspaces/${workspaceId}`);
  });
}

async function getWorkspaceResources(workspaceId: string): Promise<WorkspaceResources> {
  return serviceCall(async () => {
    const response = await apiClient.get<WorkspaceResources>(
      `/workspaces/${workspaceId}/resources`,
    );
    return ensureResponse(response, 'Failed to fetch project resources');
  });
}

async function getDiskUsage(refresh = false): Promise<WorkspaceDiskUsage[]> {
  return serviceCall(async () => {
    const response = await apiClient.get<WorkspaceDiskUsage[]>(
      `/workspaces/disk-usage${refresh ? '?refresh=true' : ''}`,
    );
    return response ?? [];
  });
}

// Where threads without a project run; the server makes it on first use.
async function getScratchWorkspace(): Promise<Workspace> {
  return serviceCall(async () => {
    const response = await apiClient.get<Workspace>('/workspaces/scratch');
    return ensureResponse(response, 'Failed to prepare a thread without a project');
  });
}

// refresh re-measures the home and projects sizes (cached for minutes).
async function getBox(refresh = false): Promise<BoxInfo> {
  return serviceCall(async () => {
    const response = await apiClient.get<BoxInfo>(
      `/workspaces/box${refresh ? '?refresh=true' : ''}`,
    );
    return ensureResponse(response, 'Failed to fetch box status');
  });
}

// reset also discards the box's system layer (apt packages); home and
// projects live on the host and are kept.
async function stopBox(reset = false): Promise<BoxInfo> {
  return serviceCall(async () => {
    const response = await apiClient.post<BoxInfo>(
      `/workspaces/box/stop${reset ? '?reset=true' : ''}`,
    );
    return ensureResponse(response, 'Failed to stop the box');
  });
}

// Frees the box's disk: system packages, toolchains and caches in home, and
// the files of git projects without unsaved work (cloned again on next use).
async function pruneBox(): Promise<PruneBoxResult> {
  return serviceCall(async () => {
    const response = await apiClient.post<PruneBoxResult>('/workspaces/box/prune');
    return ensureResponse(response, 'Failed to prune the box');
  });
}

export const workspaceService = {
  getBox,
  stopBox,
  pruneBox,
  getDiskUsage,
  listWorkspaces,
  getScratchWorkspace,
  createWorkspace,
  updateWorkspace,
  deleteWorkspace,
  getWorkspaceResources,
};
