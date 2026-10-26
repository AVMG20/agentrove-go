import { apiClient } from '@/lib/api';
import { buildQueryString, ensureResponse, serviceCall } from '@/services/base/BaseService';
import type { AccountUsage } from '@/types/usage.types';

// One account's plan usage; without an account, the default one's.
async function getUsage(refresh = false, profileId?: string): Promise<AccountUsage> {
  // No retries: a failed poll keeps the last numbers and the next one tries again.
  return serviceCall(
    async () => {
      const qs = buildQueryString({
        refresh: refresh || undefined,
        profile_id: profileId || undefined,
      });
      const response = await apiClient.get<AccountUsage>(`/usage${qs}`);
      return ensureResponse(response, 'Failed to load usage');
    },
    { maxRetries: 0 },
  );
}

export const usageService = {
  getUsage,
};
