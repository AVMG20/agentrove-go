import { Navigate } from 'react-router-dom';
import type { ReactElement } from 'react';

interface AuthRouteProps {
  isAuthenticated: boolean;
  children: ReactElement;
}

// Gate for the signed-out pages (login, sign-up, password reset): a signed-in
// user is sent into the app instead. The app itself is gated by AppShellRoute.
export function AuthRoute({ isAuthenticated, children }: AuthRouteProps) {
  if (isAuthenticated) return <Navigate to="/" replace />;
  return children;
}
