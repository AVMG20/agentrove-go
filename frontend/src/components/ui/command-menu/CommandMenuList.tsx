import { Fragment, type ReactNode, type Ref, type MouseEvent as ReactMouseEvent } from 'react';
import { GitBranch, MessageSquare } from 'lucide-react';
import { HighlightMatch } from '../shared/HighlightMatch/HighlightMatch';
import type { ThemeMeta } from '@/utils/theme';
import type { Theme } from '@/types/ui.types';
import type { GitBranchesData } from '@/types/sandbox.types';
import { MenuRow } from './MenuRow';
import type { MenuListItem, MenuMode, SettingRowItem } from './commandRegistry';
import styles from './CommandMenuList.module.scss';

interface CommandMenuListProps {
  mode: MenuMode;
  listId: string;
  activeIndex: number;
  activeItemRef: Ref<HTMLDivElement>;
  onActivate: (index: number, e: ReactMouseEvent) => void;
  query: string;
  trimmedQuery: string;
  listItems: MenuListItem[];
  onOpenChat: (id: string) => void;
  onSelectSetting: (setting: SettingRowItem) => void;
  filteredBranches: string[];
  branchesData: GitBranchesData | undefined;
  sandboxId: string | undefined;
  checkoutPending: boolean;
  onSelectBranch: (branch: string) => void;
  filteredThemes: ThemeMeta[];
  theme: Theme;
  onSelectTheme: (value: Theme) => void;
  // Message-content matches rendered after the list ("All").
  messageResults?: ReactNode;
}

export function CommandMenuList({
  mode,
  listId,
  activeIndex,
  activeItemRef,
  onActivate,
  query,
  trimmedQuery,
  listItems,
  onOpenChat,
  onSelectSetting,
  filteredBranches,
  branchesData,
  sandboxId,
  checkoutPending,
  onSelectBranch,
  filteredThemes,
  theme,
  onSelectTheme,
  messageResults,
}: CommandMenuListProps) {
  const renderMainRow = (item: MenuListItem, index: number) => {
    const rowProps = {
      index,
      isActive: index === activeIndex,
      itemRef: index === activeIndex ? activeItemRef : undefined,
      onActivate,
      id: `menu-item-${index}`,
    };
    // Sections are contiguous by kind, so a header renders on each kind transition.
    const prevKind = listItems[index - 1]?.kind;
    const showHeader = mode === 'all' && prevKind !== item.kind;

    if (item.kind === 'chat') {
      const { chat } = item;
      return (
        <Fragment key={`chat-${chat.id}`}>
          {showHeader && (
            <p className={styles['section-header']}>{trimmedQuery ? 'Chats' : 'Recent chats'}</p>
          )}
          <MenuRow {...rowProps} onSelect={() => onOpenChat(chat.id)}>
            <MessageSquare className={styles['row-icon']} />
            <HighlightMatch
              text={chat.title}
              searchQuery={query}
              className={styles['chat-title']}
            />
            {chat.workspaceName && <span className={styles.meta}>{chat.workspaceName}</span>}
          </MenuRow>
        </Fragment>
      );
    }

    const { setting } = item;
    const Icon = setting.icon;
    return (
      <Fragment key={`setting-${setting.id}`}>
        {showHeader && <p className={styles['section-header']}>Settings</p>}
        <MenuRow {...rowProps} onSelect={() => onSelectSetting(setting)}>
          <Icon className={styles['row-icon']} />
          <HighlightMatch
            text={setting.label}
            searchQuery={query}
            className={styles['command-label']}
          />
        </MenuRow>
      </Fragment>
    );
  };

  return (
    <div className={styles['list-body']} role="listbox" id={listId}>
      {mode === 'branches' ? (
        <>
          {filteredBranches.map((branch, index) => (
            <MenuRow
              key={branch}
              id={`branch-item-${index}`}
              index={index}
              isActive={index === activeIndex}
              itemRef={index === activeIndex ? activeItemRef : undefined}
              onActivate={onActivate}
              onSelect={() => onSelectBranch(branch)}
              disabled={checkoutPending}
            >
              <GitBranch className={styles['row-icon']} />
              <HighlightMatch
                text={branch}
                searchQuery={query}
                className={styles['branch-label']}
              />
              {branch === branchesData?.current_branch && <span className={styles['active-dot']} />}
            </MenuRow>
          ))}
          {filteredBranches.length === 0 && (
            <p className={styles.empty}>
              {!sandboxId
                ? 'No project selected'
                : !branchesData
                  ? 'Loading branches…'
                  : !branchesData.is_git_repo
                    ? 'Not a git repository'
                    : branchesData.branches.length === 0
                      ? 'No branches in this repository'
                      : 'No matching branches'}
            </p>
          )}
        </>
      ) : mode === 'themes' ? (
        <>
          {filteredThemes.map((themeItem, index) => {
            const Icon = themeItem.icon;
            return (
              <MenuRow
                key={themeItem.value}
                id={`theme-item-${index}`}
                index={index}
                isActive={index === activeIndex}
                itemRef={index === activeIndex ? activeItemRef : undefined}
                onActivate={onActivate}
                onSelect={() => onSelectTheme(themeItem.value)}
              >
                <Icon className={styles['row-icon']} />
                <HighlightMatch
                  text={themeItem.label}
                  searchQuery={query}
                  className={styles['theme-label']}
                />
                {themeItem.value === theme && <span className={styles['active-dot']} />}
              </MenuRow>
            );
          })}
          {filteredThemes.length === 0 && <p className={styles.empty}>No matching themes</p>}
        </>
      ) : (
        <>
          {listItems.map(renderMainRow)}
          {listItems.length === 0 && !messageResults && (
            <p className={styles.empty}>
              {mode === 'chats'
                ? 'No matching chats'
                : mode === 'settings'
                  ? 'No matching settings'
                  : 'No results'}
            </p>
          )}
          {messageResults}
        </>
      )}
    </div>
  );
}
