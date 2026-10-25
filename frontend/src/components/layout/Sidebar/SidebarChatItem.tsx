import { memo, useEffect, useRef, type MouseEvent } from 'react';
import { Zap } from 'lucide-react';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { ChatStatusDot } from '@/components/ui/ChatStatusDot/ChatStatusDot';
import { chatStatusTone, CHAT_STATUS_LABEL, type ChatStatusTone } from '@/utils/message';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
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
}: SidebarChatItemProps) {
  // Keep the active chat visible when navigation lands on an off-screen row
  const rowRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (isActive) rowRef.current?.scrollIntoView({ block: 'nearest' });
  }, [isActive]);
  // The open chat is being read — no unread signal for it
  const isUnread = !isActive && chat.unread;
  const agentKind = useChatAgentKind(chat.id, chat.session_agent_kind, chat.provider);
  const isAutomationRun = !!chat.automation_id;
  // A bolt marks automation runs in the provider icon's place.
  const anchorIcon = isAutomationRun ? (
    <Zap aria-hidden className={clsx(styles['provider-icon'], styles['automation-icon'])} />
  ) : agentKind ? (
    <ProviderIcon provider={agentKind} className={styles['provider-icon']} />
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
  const hasLeadingSlot = anchorIcon != null || status != null;
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
  const handleSelect = (e: MouseEvent<HTMLElement>) => {
    if (e.shiftKey && onOpenInSplit && canOpenInSplit && !isActive) {
      e.preventDefault();
      onOpenInSplit(chat.id);
      return;
    }
    onSelect(chat.id);
  };
  return (
    <SidebarRow
      ref={rowRef}
      isActive={isActive}
      isRevealed={isDropdownOpen}
      onRowClick={handleSelect}
      leading={restingIcon}
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
            onClick={handleSelect}
          />
        </FloatingTooltip>
      }
      subtitle={
        workspaceBadge && (
          <span
            className={clsx(
              styles['workspace-badge'],
              hasLeadingSlot && styles['workspace-badge--indented'],
            )}
          >
            <span className={styles['workspace-name']}>{workspaceBadge.name}</span>
          </span>
        )
      }
      timestamp={getRelativeTime(chat.updated_at)}
      dropdownLabel="Chat options"
      onDropdownClick={(e) => onDropdownClick(e, chat)}
    />
  );
});
