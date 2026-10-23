import { BrowserRouter, Navigate, Routes, Route } from 'react-router-dom';
import { settingsPath } from '@/components/settings/settingsSections';
import { useEffect, useState, Suspense } from 'react';
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
import { useNotificationPermissionPrompt } from '@/hooks/useNotificationPermissionPrompt';
import { PALETTES } from '@/styles/palettes';
import { ErrorBoundary } from '@/components/ui/ErrorBoundary/ErrorBoundary';

const LandingPage = lazyNamed(() => import('@/pages/LandingPage/LandingPage'), 'LandingPage');
const ChatPage = lazyNamed(() => import('@/pages/ChatPage/ChatPage'), 'ChatPage');
const ChannelPage = lazyNamed(() => import('@/pages/ChannelPage/ChannelPage'), 'ChannelPage');
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

  useLocalStreamRestoration({ enabled: isSessionAuthenticated });
  useChatEvents({ enabled: isSessionAuthenticated });
  usePageResumeRefresh({ enabled: isSessionAuthenticated });
  useNotificationPermissionPrompt(isSessionAuthenticated);

  const showLoading = hasToken && isLoading;

  return (
    <Suspense fallback={<LoadingScreen />}>
      <Routes>
        <Route
          path="/login"
          element={
            <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={false}>
              <LoginPage />
            </AuthRoute>
          }
        />
        <Route
          path="/signup"
          element={
            <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={false}>
              <SignupPage />
            </AuthRoute>
          }
        />
        <Route path="/verify-email" element={<EmailVerificationPage />} />
        {/* Outside the app shell and auth gate: public artifacts open signed out. */}
        <Route path="/artifacts/:artifactId" element={<ArtifactPage />} />
        <Route
          path="/forgot-password"
          element={
            <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={false}>
              <ForgotPasswordPage />
            </AuthRoute>
          }
        />
        <Route
          path="/reset-password"
          element={
            <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={false}>
              <ResetPasswordPage />
            </AuthRoute>
          }
        />
        <Route element={<AppShellRoute showLoading={showLoading} />}>
          <Route path="/" element={<LandingPage />} />
          <Route
            path="/chat/:chatId"
            element={
              <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={true}>
                <ChatPage />
              </AuthRoute>
            }
          />
          <Route
            path="/channels/:channelId"
            element={
              <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={true}>
                <ChannelPage />
              </AuthRoute>
            }
          />
          <Route path="/settings" element={<Navigate to={settingsPath()} replace />} />
          <Route
            path="/settings/:section"
            element={
              <AuthRoute isAuthenticated={isSessionAuthenticated} requireAuth={true}>
                <SettingsPage />
              </AuthRoute>
            }
          />
        </Route>
      </Routes>
    </Suspense>
  );
}

export default function App() {
  const resolvedTheme = useResolvedTheme();
  const theme = useUIStore((state) => state.theme);
  const [authHydrated, setAuthHydrated] = useState(false);

  useGlobalStream({ enabled: authHydrated });

  useMountEffect(() => {
    useAuthStore.getState().setAuthenticated(!!authStorage.getToken());
    setAuthHydrated(true);
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
