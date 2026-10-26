import { BrowserRouter, Navigate, Routes, Route } from 'react-router-dom';
import { settingsPath } from '@/components/settings/settingsSections';
import { useEffect, useState, Suspense, type ReactElement } from 'react';
import { lazyNamed } from '@/utils/lazyNamed';
import { useMountEffect } from '@/hooks/useMountEffect';
import { useAppBadge } from '@/hooks/useAppBadge';
import { Toaster } from 'react-hot-toast';
import { useAuthStore } from '@/store/authStore';
import { useResolvedTheme } from '@/hooks/useResolvedTheme';
import { useUIStore } from '@/store/uiStore';
import { useCurrentUserQuery } from '@/hooks/queries/useAuthQueries';
import { LoadingScreen } from '@/components/ui/LoadingScreen/LoadingScreen';
import { useGlobalStream } from '@/hooks/useGlobalStream';
import { useLocalStreamRestoration } from '@/hooks/useLocalStreamRestoration';
import { useChatEvents } from '@/hooks/useChatEvents';
import { usePageResumeRefresh } from '@/hooks/usePageResumeRefresh';
import { authService } from '@/services/authService';
import { toasterConfig } from '@/config/toaster';
import { AuthRoute } from '@/components/routes/AuthRoute/AuthRoute';
import { AppShellRoute } from '@/components/routes/AppShellRoute/AppShellRoute';
import { authStorage } from '@/utils/storage';
import { watchRemoteSignOut } from '@/utils/authSession';
import { useNotificationPermissionPrompt } from '@/hooks/useNotificationPermissionPrompt';
import { PALETTES } from '@/styles/palettes';
import { ErrorBoundary } from '@/components/ui/ErrorBoundary/ErrorBoundary';

const LandingPage = lazyNamed(() => import('@/pages/LandingPage/LandingPage'), 'LandingPage');
const ChatPage = lazyNamed(() => import('@/pages/ChatPage/ChatPage'), 'ChatPage');
const LoginPage = lazyNamed(() => import('@/pages/LoginPage/LoginPage'), 'LoginPage');
const SignupPage = lazyNamed(() => import('@/pages/SignupPage/SignupPage'), 'SignupPage');
const EmailVerificationPage = lazyNamed(
  () => import('@/pages/EmailVerificationPage/EmailVerificationPage'),
  'EmailVerificationPage',
);
const ForgotPasswordPage = lazyNamed(
  () => import('@/pages/ForgotPasswordPage/ForgotPasswordPage'),
  'ForgotPasswordPage',
);
const ResetPasswordPage = lazyNamed(
  () => import('@/pages/ResetPasswordPage/ResetPasswordPage'),
  'ResetPasswordPage',
);
const SettingsPage = lazyNamed(() => import('@/pages/SettingsPage/SettingsPage'), 'SettingsPage');
const ArtifactPage = lazyNamed(() => import('@/pages/ArtifactPage/ArtifactPage'), 'ArtifactPage');

function AppContent() {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const hasToken = !!authService.getToken();
  const isSessionAuthenticated = isAuthenticated && hasToken;
  const { data: user, isLoading } = useCurrentUserQuery({
    enabled: hasToken,
    retry: false,
  });

  // Sync auth via effect (not render): Zustand is optimistic on first paint; correcting after
  // the user query must not set external store state during render (cascading updates).
  useEffect(() => {
    if (hasToken && user) {
      useAuthStore.getState().setAuthenticated(true);
    } else if (isAuthenticated && !hasToken) {
      useAuthStore.getState().setAuthenticated(false);
    }
  }, [user, hasToken, isAuthenticated]);

  useGlobalStream({ enabled: isSessionAuthenticated });
  useLocalStreamRestoration({ enabled: isSessionAuthenticated });
  useChatEvents({ enabled: isSessionAuthenticated });
  usePageResumeRefresh({ enabled: isSessionAuthenticated });
  useNotificationPermissionPrompt(isSessionAuthenticated);

  const signedOutOnly = (page: ReactElement) => (
    <AuthRoute isAuthenticated={isSessionAuthenticated}>{page}</AuthRoute>
  );

  return (
    <Suspense fallback={<LoadingScreen />}>
      <Routes>
        <Route path="/login" element={signedOutOnly(<LoginPage />)} />
        <Route path="/signup" element={signedOutOnly(<SignupPage />)} />
        <Route path="/register" element={<Navigate to="/signup" replace />} />
        <Route path="/verify-email" element={<EmailVerificationPage />} />
        <Route path="/forgot-password" element={signedOutOnly(<ForgotPasswordPage />)} />
        <Route path="/reset-password" element={signedOutOnly(<ResetPasswordPage />)} />
        {/* Outside the auth gate: artifacts published as public open signed out
            (private ones send you to sign in from the page itself). */}
        <Route path="/artifacts/:artifactId" element={<ArtifactPage />} />

        {/* Everything else is the signed-in app; signed out, it all lands on /login. */}
        <Route
          element={
            <AppShellRoute
              isAuthenticated={isSessionAuthenticated}
              isLoading={hasToken && isLoading}
            />
          }
        >
          <Route path="/" element={<LandingPage />} />
          <Route path="/chat/:chatId" element={<ChatPage />} />
          <Route path="/settings" element={<Navigate to={settingsPath()} replace />} />
          <Route path="/settings/:section" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Route>
      </Routes>
    </Suspense>
  );
}

export default function App() {
  const resolvedTheme = useResolvedTheme();
  const theme = useUIStore((state) => state.theme);
  const [authHydrated, setAuthHydrated] = useState(false);

  useMountEffect(() => {
    useAuthStore.getState().setAuthenticated(!!authStorage.getToken());
    setAuthHydrated(true);
    return watchRemoteSignOut();
  });

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', resolvedTheme);
    // data-palette selects CSS-var overrides; light/dark/system have no override block.
    document.documentElement.setAttribute('data-palette', theme);
    const metaThemeColor = document.querySelector('meta[name="theme-color"]');
    if (metaThemeColor) {
      const palette =
        theme === 'light' || theme === 'dark' || theme === 'system' ? null : PALETTES[theme];
      metaThemeColor.setAttribute(
        'content',
        palette?.surface ?? (resolvedTheme === 'dark' ? '#0a0a0b' : '#ffffff'),
      );
    }
  }, [resolvedTheme, theme]);

  useAppBadge();

  if (!authHydrated) {
    return <LoadingScreen />;
  }

  return (
    <BrowserRouter>
      <Toaster {...toasterConfig} />
      <ErrorBoundary>
        <AppContent />
      </ErrorBoundary>
    </BrowserRouter>
  );
}
