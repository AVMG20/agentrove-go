import { useQuery, type QueryClient } from '@tanstack/react-query';
import { useMemo } from 'react';
import { profileService } from '@/services/profileService';
import type { Profile, ProfileInput, ProfileLogin } from '@/types/profile.types';
import { createMutation } from './createMutation';
import { queryKeys } from './queryKeys';

const LOGIN_POLL_MS = 1500;

// A profile's credentials or URLs feed its plan-usage meter too.
const invalidateProfiles = async (queryClient: QueryClient): Promise<void> => {
  await Promise.all([
    queryClient.invalidateQueries({ queryKey: queryKeys.profiles }),
    queryClient.invalidateQueries({ queryKey: queryKeys.claudeUsage }),
  ]);
};

export const useProfilesQuery = (options?: { enabled?: boolean }) =>
  useQuery<Profile[]>({
    queryKey: queryKeys.profiles,
    queryFn: () => profileService.listProfiles(),
    staleTime: 30_000,
    enabled: options?.enabled ?? true,
  });

// Profile lookup by id plus the default, for pickers.
export function useProfileMap(enabled = true) {
  const { data: profiles, isLoading, error } = useProfilesQuery({ enabled });
  return useMemo(() => {
    const list = profiles ?? [];
    return {
      profiles: list,
      byId: new Map(list.map((p) => [p.id, p])),
      defaultProfile: list.find((p) => p.is_default) ?? list[0] ?? null,
      isLoading,
      error,
    };
  }, [profiles, isLoading, error]);
}

export const useCreateProfileMutation = createMutation<Profile, Error, ProfileInput>(
  (data) => profileService.createProfile(data),
  invalidateProfiles,
);

export const useUpdateProfileMutation = createMutation<
  Profile,
  Error,
  { profileId: string; data: ProfileInput }
>(({ profileId, data }) => profileService.updateProfile(profileId, data), invalidateProfiles);

export const useDeleteProfileMutation = createMutation<void, Error, string>(
  (profileId) => profileService.deleteProfile(profileId),
  invalidateProfiles,
);

export const useSetDefaultProfileMutation = createMutation<Profile, Error, string>(
  (profileId) => profileService.setDefaultProfile(profileId),
  invalidateProfiles,
);

export const useStartProfileLoginMutation = createMutation<ProfileLogin, Error, string>(
  (profileId) => profileService.startLogin(profileId),
  (queryClient, data, profileId) => {
    queryClient.setQueryData(queryKeys.profileLogin(profileId), data);
  },
);

export const useSendProfileLoginInputMutation = createMutation<
  ProfileLogin,
  Error,
  { profileId: string; text: string }
>(
  ({ profileId, text }) => profileService.sendLoginInput(profileId, text),
  (queryClient, data, { profileId }) => {
    queryClient.setQueryData(queryKeys.profileLogin(profileId), data);
  },
);

export const useCancelProfileLoginMutation = createMutation<void, Error, string>(
  (profileId) => profileService.cancelLogin(profileId),
  (queryClient, _data, profileId) => {
    queryClient.removeQueries({ queryKey: queryKeys.profileLogin(profileId) });
    // The list embeds each profile's sign-in state.
    return invalidateProfiles(queryClient);
  },
);

// Polls a sign-in while it runs.
export const useProfileLoginQuery = (profileId: string, enabled: boolean) =>
  useQuery<ProfileLogin>({
    queryKey: queryKeys.profileLogin(profileId),
    queryFn: () => profileService.getLogin(profileId),
    enabled,
    retry: false,
    refetchInterval: (query) => (query.state.data?.status === 'running' ? LOGIN_POLL_MS : false),
  });
