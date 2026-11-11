import clsx from 'clsx';
import { createPortal } from 'react-dom';
import { Button } from '../primitives/Button/Button';
import { ChatSearchPanel } from '@/components/chat/chat-search/ChatSearchPanel';
import { CommandMenuInput } from './CommandMenuInput';
import { CommandMenuList } from './CommandMenuList';
import { useCommandMenu } from './useCommandMenu';
import { FILTER_LABELS, isMainMode } from './commandMenuModes';
import { MAIN_FILTERS } from './commandRegistry';
import styles from './CommandMenu.module.scss';

export function CommandMenu() {
  const {
    isOpen,
    close,
    mode,
    switchMode,
    switchFilter,
    query,
    setQuery,
    setActiveIndex,
    activeIndex,
    inputRef,
    activeItemRef,
    listId,
    activeDescendant,
    activateFromMouse,
    trimmedQuery,
    isMobile,
    listItems,
    handleOpenChatResult,
    handleSelectSetting,
    filteredBranches,
    branchesData,
    sandboxId,
    checkoutBranch,
    handleSelectBranch,
    filteredThemes,
    theme,
    handleSelectTheme,
    modKey,
  } = useCommandMenu();

  if (!isOpen) return null;

  // Phones have no Escape key and the overlay around the dialog is a thin strip,
  // so the dialog carries its own way out: Cancel beside the search field.
  const cancelButton = isMobile && (
    <Button
      variant="unstyled"
      onMouseDown={(e) => e.preventDefault()}
      onClick={close}
      className={styles.cancel}
    >
      Cancel
    </Button>
  );

  return createPortal(
    <div
      className={styles.overlay}
      onClick={close}
      role="dialog"
      aria-modal="true"
      aria-label="Command menu"
    >
      <div
        className={styles.dialog}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
      >
        <CommandMenuInput
          mode={mode}
          query={query}
          onQueryChange={(value) => {
            setQuery(value);
            setActiveIndex(0);
          }}
          inputRef={inputRef}
          onBack={() => switchMode('all')}
          listId={listId}
          activeDescendant={activeDescendant}
          trailing={cancelButton}
        />

        {isMainMode(mode) && (
          <div className={styles['filters-row']}>
            <div className={styles.filters}>
              {MAIN_FILTERS.map((filter) => (
                <Button
                  key={filter}
                  variant="unstyled"
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => switchFilter(filter)}
                  className={clsx(styles.filter, filter === mode && styles['filter--active'])}
                >
                  {FILTER_LABELS[filter]}
                </Button>
              ))}
            </div>
          </div>
        )}

        {mode === 'messages' ? (
          <div className={styles.panel}>
            <ChatSearchPanel query={query} onOpenChat={handleOpenChatResult} />
          </div>
        ) : (
          <CommandMenuList
            mode={mode}
            listId={listId}
            activeIndex={activeIndex}
            activeItemRef={activeItemRef}
            onActivate={activateFromMouse}
            query={query}
            trimmedQuery={trimmedQuery}
            listItems={listItems}
            onOpenChat={handleOpenChatResult}
            onSelectSetting={handleSelectSetting}
            filteredBranches={filteredBranches}
            branchesData={branchesData}
            sandboxId={sandboxId}
            checkoutPending={checkoutBranch.isPending}
            onSelectBranch={handleSelectBranch}
            filteredThemes={filteredThemes}
            theme={theme}
            onSelectTheme={handleSelectTheme}
            // "All" also searches message content, below the title and settings matches.
            messageResults={
              mode === 'all' && trimmedQuery.length >= 2 ? (
                <ChatSearchPanel
                  query={query}
                  onOpenChat={handleOpenChatResult}
                  inline
                  hideEmpty={listItems.length > 0}
                  heading={<p className={styles['section-header']}>Messages</p>}
                />
              ) : null
            }
          />
        )}

        {!isMobile && (
          <div className={styles.footer}>
            <span className={styles['footer-hint']}>
              {mode === 'branches'
                ? '↵ Switch branch · Esc to close'
                : mode === 'themes'
                  ? '↵ Set theme · Esc to close'
                  : mode === 'messages'
                    ? `${modKey}[ or ${modKey}] change filter · Esc to close`
                    : `↑↓ Select · ↵ Open · ${modKey}[ or ${modKey}] change filter · Esc to close`}
            </span>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
