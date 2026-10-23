import { apiClient } from '@/lib/api';
import { ensureResponse, withAuth } from '@/services/base/BaseService';
import type { Model } from '@/types/chat.types';

async function getModels(): Promise<Model[]> {
  return withAuth(async () => {
    const response = await apiClient.get<Model[]>('/models/');
    return ensureResponse(response, 'Failed to fetch models');
  });
}

export const modelService = {
  getModels,
};
