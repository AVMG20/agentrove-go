import { type ReactNode, useEffect } from 'react';
import clsx from 'clsx';
import { useMatch } from 'react-router-dom';
import { TitleBar } from '@/components/layout/TitleBar/TitleBar';
import { AppSidebar } from '@/components/layout/Sidebar/AppSidebar';
import { MobileTabBar } from '@/components/layout/MobileTabBar/MobileTabBar';
import { Link } from '@/components/ui/primitives/Link/Link';
import { useUIStore } from '@/store/uiStore';
import { useDrawerSwipe } from '@/hooks/useDrawerSwipe';
import { isMobileViewport, useIsMobile } from '@/hooks/useIsMobile';
import { useMountEffect } from '@/hooks/useMountEffect';
import { useKeyboardViewport } from '@/hooks/useKeyboardViewport';
import styles from './Layout.module.scss';

function useSidebarWidthVar() {
  useEffect(() => {
    const apply = (width: number) => {
      document.documentElement.style.setProperty('--sidebar-width', `${width}px`);
    };
    apply(useUIStore.getState().sidebarWidth);
    return useUIStore.subscribe((state, prev) => {
      if (state.sidebarWidth !== prev.sidebarWidth) apply(state.sidebarWidth);
    });
  }, []);
}

// The sidebar slides with a transition only on an explicit open/close; the
// first paint and window resizes snap into place (duration defaults to 0ms).
// The phone drawer travels further, over the page, so it takes a little longer.
const SIDEBAR_TOGGLE_MS = 220;
const DRAWER_TOGGLE_MS = 300;

function useSidebarToggleTransition() {
  useEffect(() => {
    const root = document.documentElement;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = useUIStore.subscribe((state, prev) => {
      if (state.sidebarOpen === prev.sidebarOpen) return;
      const ms = isMobileViewport() ? DRAWER_TOGGLE_MS : SIDEBAR_TOGGLE_MS;
      root.style.setProperty('--sidebar-transition-duration', `${ms}ms`);
      clearTimeout(timer);
      timer = setTimeout(() => root.style.removeProperty('--sidebar-transition-duration'), ms + 50);
    });
    return () => {
      unsubscribe();
      clearTimeout(timer);
      root.style.removeProperty('--sidebar-transition-duration');
    };
  }, []);
}

// Escape closes the phone drawer (a tablet or phone with a hardware keyboard),
// unless a dialog is up and takes the key itself.
function useEscapeClosesDrawer(active: boolean) {
  useEffect(() => {
    if (!active) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      if (document.querySelector('[aria-modal="true"]')) return;
      useUIStore.getState().setSidebarOpen(false);
    };
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [active]);
}

// The signed-in app shell: one persistent sidebar (threads or settings
// sections, depending on the route) beside a title bar and the routed page.
// It mounts once, so navigating only swaps the outlet and the sidebar body.
export function Layout({ children }: { children: ReactNode }) {
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);
  const isMobile = useIsMobile();
  // Pages with workspace views (chat, new thread) get the phone tab bar.
  const isChat = useMatch('/chat/:chatId') != null;
  const isLanding = useMatch('/') != null;
  const showTabBar = isMobile && (isChat || isLanding);

  useSidebarWidthVar();
  useSidebarToggleTransition();
  useKeyboardViewport(isMobile);

  // The open state is persisted for the docked desktop sidebar; on a phone the
  // drawer should never greet you open over the page.
  useMountEffect(() => {
    if (isMobileViewport()) useUIStore.getState().setSidebarOpen(false);
  });

  useDrawerSwipe(isMobile);
  useEscapeClosesDrawer(isMobile && sidebarOpen);

  return (
    <div className={styles.layout}>
      <Link
        href="#main-content"
        variant="unstyled"
        className={clsx('sr-only', styles['skip-link'])}
      >
        Skip to main content
      </Link>

      {isMobile && (
        // Always mounted on a phone so it can fade with the drawer as it's dragged.
        <div
          className={clsx(styles.scrim, sidebarOpen && styles['scrim--open'])}
          onClick={() => useUIStore.getState().setSidebarOpen(false)}
          aria-hidden="true"
          data-drawer-scrim
        />
      )}

      <AppSidebar />

      <div className={styles.column}>
        <TitleBar />
        <main
          id="main-content"
          className={clsx(styles.main, showTabBar && styles['main--above-tab-bar'])}
        >
          {children}
        </main>
        {showTabBar && <MobileTabBar />}
      </div>
    </div>
  );
}
