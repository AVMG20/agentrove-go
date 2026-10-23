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
