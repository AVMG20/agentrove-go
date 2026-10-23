import { memo, useEffect, useRef } from 'react';
import { ChevronRight, CornerDownRight, Zap } from 'lucide-react';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { ChatStatusDot } from '@/components/ui/ChatStatusDot/ChatStatusDot';
import { chatStatusTone, CHAT_STATUS_LABEL, type ChatStatusTone } from '@/utils/message';
import { Button } from '@/components/ui/primitives/Button/Button';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ClaudeIcon } from '@/components/ui/icons/ClaudeIcon';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import clsx from 'clsx';
import { stripMarkdownTitle } from '@/utils/format';
import { getRelativeTime } from '@/utils/date';
import type { Chat } from '@/types/chat.types';
import type { WorkspaceBadge } from '@/hooks/queries/useSidebarChatLists';
import { SidebarRow, SidebarRowTitle } from './SidebarRow';
import styles from './SidebarChatItem.module.scss';

interface SidebarChatItemProps {
  chat: Chat;
  isSelected: boolean;
  isActive?: boolean;
  isDropdownOpen: boolean;
  isChatStreaming: boolean;
  isChatBlocked: boolean;
  isChatCompleted: boolean;
  workspaceBadge?: WorkspaceBadge;
  onSelect: (chatId: string) => void;
  onOpenInSplit?: (chatId: string) => void;
  canOpenInSplit?: boolean;
  onDropdownClick: (e: React.MouseEvent<HTMLButtonElement>, chat: Chat) => void;
  onWorkspaceBadgeClick: (e: React.MouseEvent<HTMLButtonElement>, workspaceId: string) => void;
  onToggleSubThreads?: (chatId: string) => void;
  isSubThreadsExpanded?: boolean;
}

export const SidebarChatItem = memo(function SidebarChatItem({
  chat,
  isSelected,
  isActive = isSelected,
  isDropdownOpen,
  isChatStreaming,
  isChatBlocked,
  isChatCompleted,
  workspaceBadge,
  onSelect,
  onOpenInSplit,
  canOpenInSplit = false,
  onDropdownClick,
  onWorkspaceBadgeClick,
  onToggleSubThreads,
  isSubThreadsExpanded,
}: SidebarChatItemProps) {
  // onToggleSubThreads is only set when sub_thread_count > 0
  const hasSubThreads = onToggleSubThreads != null;
  // Keep the active chat visible when navigation lands on an off-screen row
  const rowRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (isActive) rowRef.current?.scrollIntoView({ block: 'nearest' });
  }, [isActive]);
  // The open chat is being read — no unread signal for it
  const isUnread = !isActive && chat.unread;
  const agentKind = useChatAgentKind(chat.id, chat.session_agent_kind);
  const isAutomationRun = !!chat.automation_id;
  // A bolt marks automation runs in the provider icon's place.
  const anchorIcon = isAutomationRun ? (
    <Zap aria-hidden className={clsx(styles['provider-icon'], styles['automation-icon'])} />
  ) : agentKind ? (
    <ClaudeIcon aria-hidden className={styles['provider-icon']} />
  ) : null;
  // Persistent status on the leading icon (visible even on hover), mirroring the
  // sidebar precedence. Running shows the spinner; the other three show a dot.
  const status = chatStatusTone({
    blocked: isChatBlocked,
    streaming: isChatStreaming,
    completed: isChatCompleted,
    unread: isUnread,
  });
  const showSpinner = status === 'running';
  // The main slot shows the spinner for 'running', so it takes no dot there; every
  // other status renders its tone directly.
  const statusTone: ChatStatusTone | null = showSpinner ? null : status;
  const statusLabel = status ? CHAT_STATUS_LABEL[status] : null;
  // Mirrors the leading-slot render below — the workspace line indents to stay flush with the title
  const hasLeadingSlot = hasSubThreads || anchorIcon != null || status != null;
  const statusSlot = hasLeadingSlot ? (
    // Running swaps to a spinner; blocked/done/unread show a colored dot on the
    // icon corner (or centered when there's no provider icon to anchor it).
    <span className={styles['status-slot']}>
      {showSpinner ? <AsciiSpinner className={styles['item-spinner']} /> : anchorIcon}
      {statusTone && (
        <ChatStatusDot
          tone={statusTone}
          // Corner-overlay only when there's an icon to anchor to; otherwise
          // the dot centers in the reserved box.
          className={clsx(anchorIcon && styles['status-corner'])}
          ringClassName={styles['status-ring']}
        />
      )}
    </span>
  ) : null;
  // On sub-threaded rows the caret takes the slot on hover (CSS, via data-reveal)
  // or while expanded — overlay the status dot on it (running shows as an amber
  // dot, since there's no spinner here) so the status never vanishes.
  const caret = hasSubThreads ? (
    <Button
      onClick={() => onToggleSubThreads(chat.id)}
      aria-expanded={isSubThreadsExpanded}
      aria-label={`${isSubThreadsExpanded ? 'Collapse' : 'Expand'} ${chat.sub_thread_count} sub-threads`}
      variant="unstyled"
      className={styles['caret-btn']}
      data-reveal={isSubThreadsExpanded ? undefined : 'hover'}
    >
      <span className={styles['caret-slot']}>
        <ChevronRight
          className={clsx(styles.chevron, isSubThreadsExpanded && styles['chevron--expanded'])}
        />
        {status && (
          <ChatStatusDot
            tone={status}
            className={styles['status-corner']}
            ringClassName={styles['status-ring']}
          />
        )}
      </span>
    </Button>
  ) : null;
  const restingTooltip = [statusLabel, isAutomationRun && 'Started by an automation']
    .filter(Boolean)
    .join(' · ');
  const restingIcon = restingTooltip ? (
    // Wordless dot/spinner carries status; the word lives in a tooltip
    <FloatingTooltip content={restingTooltip} className={styles['status-tooltip']}>
      {statusSlot}
    </FloatingTooltip>
  ) : (
    statusSlot
  );
  const leadingIcon = !caret ? (
    restingIcon
  ) : isSubThreadsExpanded ? (
    caret
  ) : (
    <>
      {caret}
      <span data-reveal="rest" className={styles['resting-slot']}>
        {restingIcon}
      </span>
    </>
  );
  return (
    <SidebarRow
      ref={rowRef}
      isActive={isActive}
      isRevealed={isDropdownOpen}
      // Single leading slot keeps titles flush (caret vs icon+status).
      leading={leadingIcon}
      title={
        // Tooltip on title only — icon has its own status tooltip.
        <FloatingTooltip
          content={
            onOpenInSplit && canOpenInSplit
              ? `${chat.title} (Shift-click to open in split)`
              : chat.title
          }
          className={styles['title-tooltip']}
        >
          <SidebarRowTitle
            label={stripMarkdownTitle(chat.title)}
            isEmphasized={isActive || isUnread}
            isCurrent={isSelected}
            isWorking={isChatStreaming}
            trailing={
              // Resting sub-thread hint; the caret replaces it on hover.
              hasSubThreads &&
              !isSubThreadsExpanded && (
                <CornerDownRight data-reveal="rest" className={styles['subthread-hint']} />
              )
            }
            onClick={(e) => {
              if (e.shiftKey && onOpenInSplit && canOpenInSplit && !isActive) {
                e.preventDefault();
                onOpenInSplit(chat.id);
                return;
              }
              onSelect(chat.id);
            }}
          />
        </FloatingTooltip>
      }
      subtitle={
        // Badge opens workspace menu (flat list has no group headers for those actions).
        workspaceBadge && (
          <Button
            variant="unstyled"
            type="button"
            data-ws-dropdown-trigger
            onClick={(e) => onWorkspaceBadgeClick(e, chat.workspace_id)}
            aria-label={`${workspaceBadge.name} project options`}
            className={clsx(
              styles['workspace-badge'],
              hasLeadingSlot && styles['workspace-badge--indented'],
            )}
          >
            <span className={styles['workspace-name']}>{workspaceBadge.name}</span>
          </Button>
        )
      }
      timestamp={getRelativeTime(chat.updated_at)}
      dropdownLabel="Chat options"
      onDropdownClick={(e) => onDropdownClick(e, chat)}
    />
  );
});
