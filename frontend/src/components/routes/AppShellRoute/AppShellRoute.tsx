import { Suspense } from 'react';
import { Outlet } from 'react-router-dom';
import { Layout } from '@/components/layout/Layout/Layout';
import { LoadingScreen } from '@/components/ui/LoadingScreen/LoadingScreen';
import { viewLoadingFallback } from '@/components/ui/shared/ViewLoadingFallback/ViewLoadingFallback';

interface AppShellRouteProps {
  showLoading: boolean;
}

// Parent route for every in-app page. The shell (title bar, sidebar slot, main)
// mounts once and survives navigation, so switching chats or opening settings
// only swaps the outlet instead of rebuilding — and re-animating — the chrome.
export function AppShellRoute({ showLoading }: AppShellRouteProps) {
  if (showLoading) return <LoadingScreen />;

  return (
    <Layout>
      <Suspense fallback={viewLoadingFallback}>
        <Outlet />
      </Suspense>
    </Layout>
  );
}
