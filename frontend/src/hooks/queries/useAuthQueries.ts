import { useMutation, useQuery } from '@tanstack/react-query';
import type { UseMutationOptions, UseQueryOptions } from '@tanstack/react-query';
import { authService } from '@/services/authService';
import type { AuthConfig, AuthResponse, User } from '@/types/user.types';
import { clearAllStreamState } from '@/hooks/stream/streamRegistry';
import { resetUserScopedStores } from '@/store/resetUserStores';
import { createMutation } from './createMutation';
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

export const useSetRegistrationMutation = createMutation<AuthConfig, Error, boolean>(
  (enabled) => authService.setRegistrationEnabled(enabled),
  (queryClient, config) => {
    queryClient.setQueryData(queryKeys.auth.config, config);
  },
);

export const useSessionsQuery = () =>
  useQuery({
    queryKey: queryKeys.auth.sessions,
    queryFn: () => authService.listSessions(),
  });

export const useRevokeSessionMutation = createMutation<void, Error, string>(
  (sessionId) => authService.revokeSession(sessionId),
  (queryClient) => queryClient.invalidateQueries({ queryKey: queryKeys.auth.sessions }),
);

export const useRevokeOtherSessionsMutation = createMutation<void, Error, void>(
  () => authService.revokeOtherSessions(),
  (queryClient) => queryClient.invalidateQueries({ queryKey: queryKeys.auth.sessions }),
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
