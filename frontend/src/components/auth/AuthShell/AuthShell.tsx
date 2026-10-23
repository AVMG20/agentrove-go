import type { ReactNode } from 'react';
import { ArrowLeft } from 'lucide-react';
import { useMatch, useNavigate } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { Button } from '@/components/ui/primitives/Button/Button';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { THEME_CYCLE, getThemeMeta } from '@/utils/theme';
import styles from './AuthShell.module.scss';

function ThemeToggleButton() {
  const theme = useUIStore((state) => state.theme);
  // Icon is the current theme (matches the profile menu); tooltip names the next.
  const Icon = getThemeMeta(theme).icon;
  const next = THEME_CYCLE[(THEME_CYCLE.indexOf(theme) + 1) % THEME_CYCLE.length];
  return (
    <FloatingTooltip content={`Switch to ${getThemeMeta(next).label}`} className={styles.tooltip}>
      <Button
        onClick={() => useUIStore.getState().toggleTheme()}
        variant="unstyled"
        className={styles['theme-toggle']}
        aria-label="Toggle theme"
      >
        <Icon className={styles.icon} />
      </Button>
    </FloatingTooltip>
  );
}

// Chrome for the signed-out pages: a slim bar with a way back to sign in and
// the theme toggle. The app shell (sidebar, title bar) only exists once signed in.
export function AuthShell({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const isLoginPage = useMatch('/login');

  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <div>
          {!isLoginPage && (
            <Button onClick={() => navigate('/login')} variant="unstyled" className={styles.back}>
              <ArrowLeft className={styles.icon} />
              <span>Sign in</span>
            </Button>
          )}
        </div>
        <ThemeToggleButton />
      </header>
      <main id="main-content" className={styles.main}>
        {children}
      </main>
    </div>
  );
}
