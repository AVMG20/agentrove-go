import { apiClient } from '@/lib/api';
import { buildQueryString, ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { ClaudeUsage } from '@/types/usage.types';

// One Claude profile's plan usage; without a profile, the default Claude one's.
async function getClaudeUsage(refresh = false, profileId?: string): Promise<ClaudeUsage> {
  // No retries: a failed poll keeps the last numbers and the next one tries again.
  return serviceCall(
    async () => {
      const qs = buildQueryString({
        refresh: refresh || undefined,
        profile_id: profileId || undefined,
      });
      const response = await apiClient.get<ClaudeUsage>(`/usage/claude${qs}`);
      return ensureResponse(response, 'Failed to load Claude usage');
    },
    { maxRetries: 0 },
  );
}

export const usageService = {
  getClaudeUsage,
};
