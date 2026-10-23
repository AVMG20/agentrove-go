import { ReactNode, useCallback, useEffect, useMemo, useState } from 'react';
import clsx from 'clsx';
import { Header, type HeaderProps } from '@/components/layout/Header/Header';
import { TitleBar } from '@/components/layout/TitleBar/TitleBar';
import { Link } from '@/components/ui/primitives/Link/Link';
import { LayoutContext, type LayoutContextValue } from './layoutState';
import { useUIStore } from '@/store/uiStore';
import { useSwipeGesture } from '@/hooks/useSwipeGesture';
import { useIsMobile } from '@/hooks/useIsMobile';
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

// Sidebar/title-bar/main transitions default to 0ms (see the SCSS fallbacks) so
// route changes that mount or drop the sidebar snap into place. Only an explicit
// open/close animates: the store subscription fires synchronously inside
// setSidebarOpen, before React re-renders with the new open state.
const SIDEBAR_TOGGLE_MS = 250;

function useSidebarToggleTransition() {
  useEffect(() => {
    const root = document.documentElement;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsubscribe = useUIStore.subscribe((state, prev) => {
      if (state.sidebarOpen === prev.sidebarOpen) return;
      root.style.setProperty('--sidebar-transition-duration', `${SIDEBAR_TOGGLE_MS}ms`);
      clearTimeout(timer);
      timer = setTimeout(
        () => root.style.removeProperty('--sidebar-transition-duration'),
        SIDEBAR_TOGGLE_MS + 50,
      );
    });
    return () => {
      unsubscribe();
      clearTimeout(timer);
      root.style.removeProperty('--sidebar-transition-duration');
    };
  }, []);
}

function MobileSidebarOverlay() {
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);

  if (!sidebarOpen) return null;

  return (
    <div
      className={styles.overlay}
      onClick={() => useUIStore.getState().setSidebarOpen(false)}
      aria-hidden="true"
    />
  );
}

export interface LayoutProps extends HeaderProps {
  children: ReactNode;
  className?: string;
  contentClassName?: string;
  showHeader?: boolean;
}

export function Layout({
  children,
  isAuthPage = false,
  className,
  contentClassName,
  showHeader = true,
}: LayoutProps) {
  const [sidebarContent, setSidebarContent] = useState<ReactNode | null>(null);
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);
  const isMobile = useIsMobile();
  // Desktop-only gating lives in CSS (media query on main--pushed) so the push
  // can never disagree with the scrim/titlebar when JS isMobile state goes stale
  const shouldPushContent = !!sidebarContent && sidebarOpen;

  useSidebarWidthVar();
  useSidebarToggleTransition();

  useSwipeGesture({
    onSwipeRight: () => useUIStore.getState().setSidebarOpen(true),
    onSwipeLeft: () => sidebarOpen && useUIStore.getState().setSidebarOpen(false),
    enabled: isMobile && !!sidebarContent,
  });

  const setSidebar = useCallback((content: ReactNode | null) => {
    setSidebarContent(content);
  }, []);

  const contextValue = useMemo<LayoutContextValue>(
    () => ({
      sidebar: sidebarContent,
      setSidebar,
    }),
    [setSidebar, sidebarContent],
  );

  return (
    <LayoutContext.Provider value={contextValue}>
      <div className={clsx(styles.layout, className)}>
        <Link
          href="#main-content"
          variant="unstyled"
          className={clsx('sr-only', styles['skip-link'])}
        >
          Skip to main content
        </Link>
        <TitleBar />
        {showHeader && <Header isAuthPage={isAuthPage} />}

        <div className={styles.body}>
          {sidebarContent && <MobileSidebarOverlay />}

          {sidebarContent ? <div className={styles['sidebar-slot']}>{sidebarContent}</div> : null}

          <main
            id="main-content"
            className={clsx(
              styles.main,
              shouldPushContent && styles['main--pushed'],
              contentClassName,
            )}
          >
            {children}
          </main>
        </div>
      </div>
    </LayoutContext.Provider>
  );
}
