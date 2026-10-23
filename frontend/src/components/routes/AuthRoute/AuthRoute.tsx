import { Navigate } from 'react-router-dom';
import type { ReactElement } from 'react';

interface AuthRouteProps {
  isAuthenticated: boolean;
  requireAuth: boolean;
  children: ReactElement;
}

// Auth gate only — in-app pages get their chrome from AppShellRoute.
export function AuthRoute({ isAuthenticated, requireAuth, children }: AuthRouteProps) {
  if (requireAuth && !isAuthenticated) {
    return <Navigate to="/login" />;
  }

  if (!requireAuth && isAuthenticated) {
    return <Navigate to="/" replace />;
  }

  return children;
}
