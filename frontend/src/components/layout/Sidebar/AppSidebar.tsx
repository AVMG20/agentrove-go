import { useCallback, useEffect, useRef } from 'react';
import { useLocation, useMatch, useNavigate } from 'react-router-dom';
import clsx from 'clsx';
import { useUIStore } from '@/store/uiStore';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useCurrentUserQuery } from '@/hooks/queries/useAuthQueries';
import { useLogout } from '@/hooks/useLogout';
import { ToggleButton } from '@/components/ui/ToggleButton/ToggleButton';
import { UserProfileMenu } from '@/components/layout/UserProfileMenu/UserProfileMenu';
import { BoxStatus } from '@/components/layout/BoxStatus/BoxStatus';
import { settingsPath } from '@/components/settings/settingsSections';
import iconDark from '/brand/icon-dark.svg';
import iconLight from '/brand/icon-white.svg';
import { SidebarResizeHandle } from './SidebarResizeHandle';
import { SettingsNav } from './SettingsNav';
import { ThreadsPanel } from './ThreadsPanel';
import styles from './AppSidebar.module.scss';

// The one sidebar of the app shell. Its body follows the route: the thread
// browser everywhere, the settings sections under /settings. The thread panel
// stays mounted (hidden) while in settings so coming back keeps its scroll
// position, expanded folders and loaded pages.
export function AppSidebar() {
  const navigate = useNavigate();
  const location = useLocation();
  const isMobile = useIsMobile();
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);
  const isSettings = useMatch('/settings/*') != null;
  const { data: currentUser } = useCurrentUserQuery();
  const logout = useLogout();

  // Where "Back to threads" returns to: the last non-settings page.
  const lastThreadsPathRef = useRef('/');
  useEffect(() => {
    if (!isSettings) lastThreadsPathRef.current = location.pathname;
  }, [isSettings, location.pathname]);

  const closeOnMobile = useCallback(() => {
    if (isMobile) useUIStore.getState().setSidebarOpen(false);
  }, [isMobile]);

  return (
    <aside
      id="app-sidebar"
      aria-label={isSettings ? 'Settings navigation' : 'Threads'}
      className={clsx(styles.sidebar, sidebarOpen && styles['sidebar--open'])}
      // Closed: out of the tab order and the accessibility tree.
      inert={!sidebarOpen}
    >
      <div className={styles.header}>
        <button
          type="button"
          className={styles.brand}
          onClick={() => {
            navigate('/');
            closeOnMobile();
          }}
          aria-label="Bitbox home"
        >
          <img src={iconDark} alt="" className={clsx(styles.logo, styles['logo--on-light'])} />
          <img src={iconLight} alt="" className={clsx(styles.logo, styles['logo--on-dark'])} />
          <span className={styles['brand-name']}>Bitbox</span>
        </button>
        <ToggleButton
          isOpen
          position="left"
          onClick={() => useUIStore.getState().setSidebarOpen(false)}
          ariaLabel="Close sidebar"
        />
      </div>

      {isSettings && (
        <SettingsNav
          onBack={() => {
            navigate(lastThreadsPathRef.current);
          }}
          onNavigate={closeOnMobile}
        />
      )}
      <ThreadsPanel hidden={isSettings} />

      {/* Always rendered so settings/sign-out stay reachable if the user query fails. */}
      <div className={styles.footer}>
        <BoxStatus />
        <UserProfileMenu
          displayName={currentUser?.username || currentUser?.email || ''}
          onOpenSettings={() => navigate(settingsPath())}
          onSignOut={() => logout.mutate()}
        />
      </div>
      {!isMobile && sidebarOpen && <SidebarResizeHandle />}
    </aside>
  );
}
