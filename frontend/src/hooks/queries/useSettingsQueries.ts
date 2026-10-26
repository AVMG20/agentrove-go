import { useQuery } from '@tanstack/react-query';
import type { QueryClient, UseQueryOptions } from '@tanstack/react-query';
import { settingsService } from '@/services/settingsService';
import type { UserSettings, UserSettingsUpdate } from '@/types/user.types';
import { createMutation } from './createMutation';
import { queryKeys } from './queryKeys';

const settingsQuery = () => ({
  queryKey: [queryKeys.settings],
  queryFn: () => settingsService.getSettings(),
});

export const useSettingsQuery = (options?: Partial<UseQueryOptions<UserSettings>>) => {
  return useQuery({
    ...settingsQuery(),
    ...options,
  });
};

// Settings are never persisted to storage (they carry secrets), so a cold tab
// must fetch before a saved opt-out can be honored.
export async function fetchNotificationsEnabled(queryClient: QueryClient): Promise<boolean> {
  try {
    const settings = await queryClient.ensureQueryData(settingsQuery());
    return settings.notifications_enabled;
  } catch {
    // Settings unreachable — assume the server default (enabled).
    return true;
  }
}

export const useUpdateSettingsMutation = createMutation<UserSettings, Error, UserSettingsUpdate>(
  (data) => settingsService.updateSettings(data),
  (queryClient, data) => {
    queryClient.setQueryData([queryKeys.settings], data);
    // Env vars pick the Claude proxy (and token) the usage meter reports on.
    void queryClient.invalidateQueries({ queryKey: queryKeys.usage });
  },
);
