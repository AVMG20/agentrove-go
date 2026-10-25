import { apiClient } from '@/lib/api';
import { ensureResponse, withAuth } from '@/services/base/BaseService';
import type { AgentKind, Model } from '@/types/chat.types';

async function getModels(agentKind: AgentKind | 'all' = 'claude'): Promise<Model[]> {
  return withAuth(async () => {
    const response = await apiClient.get<Model[]>(`/models/?agent_kind=${agentKind}`);
    return ensureResponse(response, 'Failed to fetch models');
  });
}

export const modelService = {
  getModels,
};
