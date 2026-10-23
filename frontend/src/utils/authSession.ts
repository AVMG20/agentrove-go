import { authStorage } from '@/utils/storage';

let redirectInProgress = false;

export function invalidateSessionAndRedirect(): void {
  authStorage.clearAuth();

  if (typeof window === 'undefined') {
    return;
  }
  if (window.location.pathname === '/login') {
    return;
  }
  if (redirectInProgress) {
    return;
  }

  redirectInProgress = true;
  window.location.replace('/login');
}

// Another tab signed out (or its session expired): this tab's UI and caches belong to a
// dead session, so leave rather than keep acting on cleared credentials.
export function watchRemoteSignOut(): () => void {
  return authStorage.onRemoteSignOut(invalidateSessionAndRedirect);
}
