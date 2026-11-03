import { useMutation, useQuery } from '@tanstack/react-query';
import type { QueryClient, UseMutationOptions, UseQueryOptions } from '@tanstack/react-query';
import { authService } from '@/services/authService';
import type { AuthConfig, AuthResponse, AuthSession, User } from '@/types/user.types';
import { clearAllStreamState } from '@/hooks/stream/streamRegistry';
import { resetUserScopedStores } from '@/store/resetUserStores';
import { createMutation } from './createMutation';
import { snapshotQueries } from './optimistic';
import { queryKeys } from './queryKeys';

export const useCurrentUserQuery = (options?: Partial<UseQueryOptions<User>>) => {
  return useQuery({
    queryKey: [queryKeys.auth.user],
    queryFn: () => authService.getCurrentUser(),
    retry: false,
    ...options,
  });
};

export const useAuthConfigQuery = () =>
  useQuery({
    queryKey: queryKeys.auth.config,
    queryFn: () => authService.getAuthConfig(),
    staleTime: 60_000,
  });

// Optimistic, so the switch flips at once.
export const useSetRegistrationMutation = createMutation<AuthConfig, Error, boolean>(
  (enabled) => authService.setRegistrationEnabled(enabled),
  (queryClient, config, _enabled, { isLast }) => {
    if (isLast) queryClient.setQueryData(queryKeys.auth.config, config);
  },
  {
    mutationKey: ['auth-registration'],
    scope: 'auth-registration',
    apply: async (queryClient, enabled) => {
      const snapshot = await snapshotQueries(queryClient, [
        { queryKey: queryKeys.auth.config, exact: true },
      ]);
      queryClient.setQueryData<AuthConfig>(
        queryKeys.auth.config,
        (old) => old && { ...old, registration_enabled: enabled },
      );
      return snapshot;
    },
  },
);

export const useSessionsQuery = () =>
  useQuery({
    queryKey: queryKeys.auth.sessions,
    queryFn: () => authService.listSessions(),
  });

// Shared by the session revokes: only the last in flight refetches.
const sessionsMutationKey = ['auth-sessions-mutation'] as const;

async function applySessionsChange(
  queryClient: QueryClient,
  keep: (session: AuthSession) => boolean,
) {
  const snapshot = await snapshotQueries(queryClient, [
    { queryKey: queryKeys.auth.sessions, exact: true },
  ]);
  queryClient.setQueryData<AuthSession[]>(queryKeys.auth.sessions, (old) => old?.filter(keep));
  return snapshot;
}

const settleSessions = (queryClient: QueryClient) =>
  void queryClient.invalidateQueries({ queryKey: queryKeys.auth.sessions });

// Optimistic: the session leaves the list at once.
export const useRevokeSessionMutation = createMutation<void, Error, string>(
  (sessionId) => authService.revokeSession(sessionId),
  () => undefined,
  {
    mutationKey: sessionsMutationKey,
    apply: (queryClient, sessionId) =>
      applySessionsChange(queryClient, (session) => session.id !== sessionId),
    settle: settleSessions,
  },
);

// Optimistic: only this device's session stays listed.
export const useRevokeOtherSessionsMutation = createMutation<void, Error, void>(
  () => authService.revokeOtherSessions(),
  () => undefined,
  {
    mutationKey: sessionsMutationKey,
    apply: (queryClient) => applySessionsChange(queryClient, (session) => session.current),
    settle: settleSessions,
  },
);

interface LoginData {
  username: string;
  password: string;
}

export const useLoginMutation = createMutation<AuthResponse, Error, LoginData>(
  (data) => authService.login(data),
  async (queryClient) => {
    await queryClient.cancelQueries();
    queryClient.clear();
  },
);

interface SignupData {
  email: string;
  username: string;
  password: string;
}

export const useSignupMutation = createMutation<User, Error, SignupData>(
  (data) => authService.signup(data),
  async (queryClient, response) => {
    const needsVerification = response.email_verification_required && !response.is_verified;
    if (!needsVerification && authService.isAuthenticated()) {
      await queryClient.cancelQueries();
      queryClient.clear();
    }
  },
);

interface VerifyEmailData {
  token: string;
}

interface ResendVerificationData {
  email: string;
}

interface ForgotPasswordData {
  email: string;
}

interface ResetPasswordData {
  token: string;
  password: string;
}

export const useVerifyEmailMutation = (
  options?: UseMutationOptions<User, Error, VerifyEmailData>,
) => {
  return useMutation({
    mutationFn: (data: VerifyEmailData) => authService.verifyEmail(data),
    ...options,
  });
};

export const useResendVerificationMutation = (
  options?: UseMutationOptions<void, Error, ResendVerificationData>,
) => {
  return useMutation({
    mutationFn: (data: ResendVerificationData) => authService.resendVerification(data),
    ...options,
  });
};

export const useForgotPasswordMutation = (
  options?: UseMutationOptions<void, Error, ForgotPasswordData>,
) => {
  return useMutation({
    mutationFn: (data: ForgotPasswordData) => authService.forgotPassword(data),
    ...options,
  });
};

export const useResetPasswordMutation = (
  options?: UseMutationOptions<void, Error, ResetPasswordData>,
) => {
  return useMutation({
    mutationFn: (data: ResetPasswordData) => authService.resetPassword(data),
    ...options,
  });
};

export const useLogoutMutation = createMutation<void, Error, void>(
  () => authService.logout(),
  async (queryClient) => {
    await queryClient.cancelQueries();
    queryClient.clear();
    // SPA logout doesn't reload — the next session must not inherit buffers.
    clearAllStreamState();
    resetUserScopedStores();
  },
);
