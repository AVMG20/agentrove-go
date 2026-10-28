import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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

// Under the settings key so useChatEvents holds off refetching while it runs.
const sidebarFolderOrderMutationKey = [queryKeys.settings, 'sidebar-folder-order'];

// Optimistic, so a dropped folder stays where it landed. Every drop writes the
// whole list, so with drops in flight only the last response may land.
export function useUpdateSidebarFolderOrderMutation() {
  const queryClient = useQueryClient();
  const isLast = () => queryClient.isMutating({ mutationKey: sidebarFolderOrderMutationKey }) === 1;
  return useMutation({
    mutationKey: sidebarFolderOrderMutationKey,
    // One save at a time, in drop order, so the server keeps the last drop.
    scope: { id: 'sidebar-folder-order' },
    mutationFn: (order: string[]) =>
      settingsService.updateSettings({ sidebar_folder_order: order }),
    onMutate: async (order) => {
      await queryClient.cancelQueries({ queryKey: [queryKeys.settings] });
      queryClient.setQueryData<UserSettings>(
        [queryKeys.settings],
        (old) => old && { ...old, sidebar_folder_order: order },
      );
    },
    onSuccess: (data) => {
      if (isLast()) queryClient.setQueryData([queryKeys.settings], data);
    },
    onError: () => {
      if (isLast()) void queryClient.invalidateQueries({ queryKey: [queryKeys.settings] });
    },
  });
}

export const useUpdateSettingsMutation = createMutation<UserSettings, Error, UserSettingsUpdate>(
  (data) => settingsService.updateSettings(data),
  (queryClient, data) => {
    queryClient.setQueryData([queryKeys.settings], data);
    // Env vars pick the Claude proxy (and token) the usage meter reports on.
    void queryClient.invalidateQueries({ queryKey: queryKeys.usage });
  },
);
