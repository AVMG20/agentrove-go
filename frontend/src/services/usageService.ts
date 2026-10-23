import { apiClient } from '@/lib/api';
import { buildQueryString, ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { ClaudeUsage } from '@/types/usage.types';

async function getClaudeUsage(refresh = false): Promise<ClaudeUsage> {
  // No retries: a failed poll keeps the last numbers and the next one tries again.
  return serviceCall(
    async () => {
      const qs = refresh ? buildQueryString({ refresh: true }) : '';
      const response = await apiClient.get<ClaudeUsage>(`/usage/claude${qs}`);
      return ensureResponse(response, 'Failed to load Claude usage');
    },
    { maxRetries: 0 },
  );
}

export const usageService = {
  getClaudeUsage,
};
