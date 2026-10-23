import { Plus, Search } from 'lucide-react';
import { useMatch, useNavigate } from 'react-router-dom';
import { useAuthStore } from '@/store/authStore';
import { useUIStore } from '@/store/uiStore';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ToggleButton } from '@/components/ui/ToggleButton/ToggleButton';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ViewSwitcher } from '@/components/layout/ViewSwitcher/ViewSwitcher';
import { ChatTabs } from '@/components/layout/ChatTabs/ChatTabs';
import { ChannelHeader } from '@/components/channels/ChannelHeader';
import clsx from 'clsx';
import styles from './TitleBar.module.scss';

export function TitleBar() {
  const navigate = useNavigate();
  const isChatPage = useMatch('/chat/:chatId');
  const isChannelPage = useMatch('/channels/:channelId');
  const isLandingPage = useMatch('/');
  const showSidebar = isChatPage || isChannelPage || isLandingPage;
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);

  if (!(isAuthenticated && showSidebar)) return null;

  return (
    <div
      // The row owns the height (2.5rem bar + top inset); each section pads its
      // content below the inset (pt) so the section's background bleeds under the
      // iOS status bar. env() is 0 elsewhere, so the bar stays a plain h-10.
      className={styles['title-bar']}
    >
      {/* When docked, merges into the sidebar header (border-r, no hairline). */}
      <div className={clsx(styles.left, sidebarOpen && styles['left--docked'])}>
        <div className={styles['toggle-wrap']}>
          <ToggleButton
            isOpen={sidebarOpen}
            onClick={() => useUIStore.getState().setSidebarOpen(!sidebarOpen)}
            position="left"
            ariaLabel="Toggle sidebar"
          />
          {/* Search/new chat only when sidebar is closed (it already has both). */}
          {!sidebarOpen && (
            <>
              <FloatingTooltip content="Search">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={styles['icon-button']}
                  onClick={() => useUIStore.getState().setCommandMenuOpen(true)}
                  aria-label="Search"
                >
                  <Search className={styles['icon-button-glyph']} />
                </Button>
              </FloatingTooltip>
              <FloatingTooltip content="New chat">
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={styles['icon-button']}
                  onClick={() => navigate('/')}
                  aria-label="New chat"
                >
                  <Plus className={styles['icon-button-glyph']} />
                </Button>
              </FloatingTooltip>
            </>
          )}
        </div>
      </div>

      <div className={styles.band}>
        <div className={styles['tabs-area']}>
          {(isChatPage || isLandingPage) && <ChatTabs />}
          {isChannelPage && <ChannelHeader />}
        </div>
        {/* No per-view tabs — switcher opens/closes views (landing needs it too). */}
        <ViewSwitcher />
      </div>
    </div>
  );
}
