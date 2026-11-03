import { ChevronRight, Search, SquarePen } from 'lucide-react';
import clsx from 'clsx';
import { useMatch, useNavigate } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ToggleButton } from '@/components/ui/ToggleButton/ToggleButton';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ViewSwitcher } from '@/components/layout/ViewSwitcher/ViewSwitcher';
import { ChatTitle } from '@/components/layout/ChatTitle/ChatTitle';
import { useIsMobile } from '@/hooks/useIsMobile';
import { findSettingsSection } from '@/components/settings/settingsSections';
import { folderColorClass, folderIcon } from '@/components/layout/Sidebar/folderAppearance';
import { useChatQuery } from '@/hooks/queries/useChatQueries';
import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import styles from './TitleBar.module.scss';

// "project /" ahead of the desktop chat title, wearing the project's sidebar
// folder icon and color. Reads the caches the sidebar already fills.
function WorkspaceCrumb({ chatId }: { chatId: string }) {
  const { data: chat } = useChatQuery(chatId);
  const workspace = useWorkspacesList().find((w) => w.id === chat?.workspace_id);
  if (!workspace) return null;
  const FolderIcon = folderIcon(workspace.icon);
  return (
    <div className={clsx(styles.crumb, folderColorClass(workspace.color))}>
      <FolderIcon aria-hidden className={styles['crumb-icon']} />
      <span className={styles['crumb-name']} title={workspace.name}>
        {workspace.name}
      </span>
      <span aria-hidden className={styles['crumb-separator']}>
        /
      </span>
    </div>
  );
}

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

function SearchAndNewThread() {
  const navigate = useNavigate();
  return (
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
      <FloatingTooltip content="New thread">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={styles['icon-button']}
          onClick={() => navigate('/')}
          aria-label="New thread"
        >
          <SquarePen className={styles['icon-button-glyph']} />
        </Button>
      </FloatingTooltip>
    </>
  );
}

// The bar above the page. With the sidebar closed (or on mobile) it carries
// the sidebar toggle plus search/new thread, so they're never more than a
// click away; the rest is per-route: the chat title or the settings
// breadcrumb. On a phone it reads like an app's navigation bar: the drawer
// button leading, the title in the middle, the actions trailing in thumb reach.
export function TitleBar() {
  const isChatPage = useMatch('/chat/:chatId');
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
        {!isMobile && <SearchAndNewThread />}
      </div>

      <div className={styles.content}>
        {isChatPage && !isMobile && isChatPage.params.chatId && (
          <WorkspaceCrumb chatId={isChatPage.params.chatId} />
        )}
        {isChatPage && <ChatTitle />}
        {settingsMatch && <SettingsTitle sectionId={settingsMatch.params.section} />}
      </div>
      {isMobile ? (
        <div className={styles.trailing}>
          <SearchAndNewThread />
        </div>
      ) : (
        !settingsMatch && <ViewSwitcher />
      )}
    </div>
  );
}
