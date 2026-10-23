import { Suspense } from 'react';
import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { Layout } from '@/components/layout/Layout/Layout';
import { LoadingScreen } from '@/components/ui/LoadingScreen/LoadingScreen';
import { viewLoadingFallback } from '@/components/ui/shared/ViewLoadingFallback/ViewLoadingFallback';

interface AppShellRouteProps {
  isAuthenticated: boolean;
  isLoading: boolean;
}

// Parent route for every in-app page, and the auth gate in front of them:
// nothing app-related mounts (or fetches) until the session is confirmed.
// The shell mounts once and survives navigation, so switching chats or
// opening settings only swaps the outlet.
export function AppShellRoute({ isAuthenticated, isLoading }: AppShellRouteProps) {
  const location = useLocation();

  if (isLoading) return <LoadingScreen />;
  if (!isAuthenticated) {
    const here = location.pathname + location.search;
    const next = here === '/' ? '' : `?next=${encodeURIComponent(here)}`;
    return <Navigate to={`/login${next}`} replace />;
  }

  return (
    <Layout>
      <Suspense fallback={viewLoadingFallback}>
        <Outlet />
      </Suspense>
    </Layout>
  );
}
