import { ChevronRight, Plus, Search } from 'lucide-react';
import { useMatch, useNavigate } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ToggleButton } from '@/components/ui/ToggleButton/ToggleButton';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ViewSwitcher } from '@/components/layout/ViewSwitcher/ViewSwitcher';
import { ChatTabs, MobileChatTitle } from '@/components/layout/ChatTabs/ChatTabs';
import { useIsMobile } from '@/hooks/useIsMobile';
import { ChannelHeader } from '@/components/channels/ChannelHeader';
import { findSettingsSection } from '@/components/settings/settingsSections';
import styles from './TitleBar.module.scss';

function SettingsTitle({ sectionId }: { sectionId: string | undefined }) {
  const section = findSettingsSection(sectionId);
  return (
    <div className={styles.breadcrumb}>
      <span className={styles['breadcrumb-root']}>Settings</span>
      {section && (
        <>
          <ChevronRight className={styles['breadcrumb-separator']} aria-hidden />
          <span className={styles['breadcrumb-current']}>{section.label}</span>
        </>
      )}
    </div>
  );
}

// The bar above the page. With the sidebar closed (or on mobile) it carries
// the sidebar toggle plus search/new thread, so they're never more than a
// click away; the rest is per-route: chat tabs, the channel header or the
// settings breadcrumb.
export function TitleBar() {
  const navigate = useNavigate();
  const isChatPage = useMatch('/chat/:chatId');
  const isChannelPage = useMatch('/channels/:channelId');
  const isLandingPage = useMatch('/');
  const settingsMatch = useMatch('/settings/:section');
  const sidebarOpen = useUIStore((state) => state.sidebarOpen);
  const isMobile = useIsMobile();

  return (
    <div className={styles['title-bar']}>
      <div className={styles.leading} data-sidebar-open={sidebarOpen || undefined}>
        <ToggleButton
          isOpen={sidebarOpen}
          onClick={() => useUIStore.getState().setSidebarOpen(!sidebarOpen)}
          position="left"
          ariaLabel="Toggle sidebar"
        />
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
        <FloatingTooltip content="New thread">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className={styles['icon-button']}
            onClick={() => navigate('/')}
            aria-label="New thread"
          >
            <Plus className={styles['icon-button-glyph']} />
          </Button>
        </FloatingTooltip>
      </div>

      <div className={styles.content}>
        {(isChatPage || isLandingPage) &&
          (isMobile ? isChatPage && <MobileChatTitle /> : <ChatTabs />)}
        {isChannelPage && <ChannelHeader />}
        {settingsMatch && <SettingsTitle sectionId={settingsMatch.params.section} />}
      </div>
      {!settingsMatch && <ViewSwitcher />}
    </div>
  );
}
