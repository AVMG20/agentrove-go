import { useCallback, useEffect, useMemo } from 'react';
import { useMatch, useNavigate } from 'react-router-dom';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { ChatStatusDot } from '@/components/ui/ChatStatusDot/ChatStatusDot';
import type { ChatStatusTone } from '@/utils/message';
import { ChevronDown, Zap } from 'lucide-react';
import clsx from 'clsx';
import { ClaudeIcon } from '@/components/ui/icons/ClaudeIcon';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useUIStore } from '@/store/uiStore';
import { useStreamStore } from '@/store/streamStore';
import { useBlockedChatIds } from '@/hooks/useBlockedChatIds';
import { useWorkingChatIds } from '@/hooks/useWorkingChatIds';
import { useChatQuery } from '@/hooks/queries/useChatQueries';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import { useIsMobile } from '@/hooks/useIsMobile';
import { stripMarkdownTitle } from '@/utils/format';
import { paintedChatIds } from '@/utils/tileHelpers';
import { TitleBarTab, TitleBarTabs } from '@/components/layout/TitleBarTab/TitleBarTab';
import styles from './ChatTabs.module.scss';

type ChatTabStatus = 'blocked' | 'streaming' | 'completed';

const STATUS_LABELS: Record<ChatTabStatus, string> = {
  blocked: 'Needs you',
  streaming: 'Running',
  completed: 'Done',
};

function chatTabStatus(
  chatId: string,
  blocked: Set<string>,
  streaming: Set<string>,
  completed: Set<string>,
): ChatTabStatus | null {
  if (blocked.has(chatId)) return 'blocked';
  if (streaming.has(chatId)) return 'streaming';
  if (completed.has(chatId)) return 'completed';
  return null;
}

const MOBILE_STATUS_TONE: Record<ChatTabStatus, ChatStatusTone> = {
  blocked: 'blocked',
  streaming: 'running',
  completed: 'completed',
};

// Phones have no tab strip: the routed thread's title and status, opening the
// session panel as a bottom sheet.
export function MobileChatTitle() {
  const chatId = useMatch('/chat/:chatId')?.params.chatId;
  const { data: chat } = useChatQuery(chatId);
  const completedChatIds = useStreamStore((s) => s.completedChatIds);
  const blockedChatIdSet = useBlockedChatIds();
  const streamingChatIdSet = useWorkingChatIds();
  if (!chatId) return null;

  const status = chatTabStatus(chatId, blockedChatIdSet, streamingChatIdSet, completedChatIds);
  const title = chat?.title ? stripMarkdownTitle(chat.title) : '…';
  return (
    <Button
      type="button"
      variant="unstyled"
      className={styles['mobile-title']}
      onClick={() => useUIStore.getState().setSessionSheetOpen(true)}
      aria-label={`${title}${status ? ` · ${STATUS_LABELS[status]}` : ''}. Show session details`}
    >
      {status && (
        <ChatStatusDot tone={MOBILE_STATUS_TONE[status]} className={styles['tab-status-dot']} />
      )}
      <span className={styles['mobile-title-text']}>{title}</span>
      <ChevronDown aria-hidden className={styles['mobile-title-chevron']} />
    </Button>
  );
}

interface ChatTabProps {
  chatId: string;
  isActive: boolean;
  isCurrent: boolean;
  status: ChatTabStatus | null;
  onSelect: (chatId: string) => void;
  onClose: (chatId: string) => void;
}

function ChatTab({ chatId, isActive, isCurrent, status, onSelect, onClose }: ChatTabProps) {
  const chatQuery = useChatQuery(chatId);
  // Close only on confirmed 404 — transient network/auth errors must not wipe tabs.
  const isChatGone = (chatQuery.error as (Error & { status?: number }) | null)?.status === 404;
  useEffect(() => {
    if (isChatGone) useUIStore.getState().closeChatTab(chatId);
  }, [isChatGone, chatId]);

  const title = chatQuery.data?.title ? stripMarkdownTitle(chatQuery.data.title) : '…';
  // Streaming uses AsciiSpinner; blocked/completed map straight to ChatStatusDot.
  const statusDotTone: ChatStatusTone | null = status && status !== 'streaming' ? status : null;
  const agentKind = useChatAgentKind(chatId, chatQuery.data?.session_agent_kind);
  const isAutomationRun = !!chatQuery.data?.automation_id;
  const tooltip = [
    title,
    status && STATUS_LABELS[status],
    isAutomationRun && 'Started by an automation',
  ]
    .filter(Boolean)
    .join(' · ');

  return (
    <TitleBarTab
      icon={
        status === 'streaming' ? (
          <AsciiSpinner className={styles['tab-spinner']} />
        ) : statusDotTone ? (
          <ChatStatusDot tone={statusDotTone} className={styles['tab-status-dot']} />
        ) : isAutomationRun ? (
          <Zap
            aria-hidden
            className={clsx(styles['tab-provider-icon'], styles['tab-automation-icon'])}
          />
        ) : (
          agentKind && <ClaudeIcon aria-hidden className={styles['tab-provider-icon']} />
        )
      }
      title={title}
      isActive={isActive}
      actions={{
        tooltip,
        isCurrent,
        onSelect: () => onSelect(chatId),
        onClose: () => onClose(chatId),
      }}
    />
  );
}

// Working-set tabs (sidebar is the archive); re-clicking the current chat surfaces the agent pane.
export function ChatTabs() {
  const navigate = useNavigate();
  const isMobile = useIsMobile();
  // TitleBar sits outside /chat/:chatId, so read the route from the location.
  const routedChatId = useMatch('/chat/:chatId')?.params.chatId;
  const chatTabs = useUIStore((s) => s.chatTabs);
  const splitChatIds = useUIStore((s) => s.splitChatIds);
  const visibleLayout = useUIStore((s) => s.visibleLayout);
  // Marked on stream success; sidebar clears when the chat is viewed.
  const completedChatIds = useStreamStore((s) => s.completedChatIds);
  // Pending plan/permission requests or agent question forms — blocked on the user.
  const blockedChatIdSet = useBlockedChatIds();

  // From layout (not splitChatIds): a split chat under a full-screen primary is off-screen.
  const visibleChatIds = useMemo(
    () => paintedChatIds(visibleLayout.flat(), splitChatIds, routedChatId),
    [visibleLayout, routedChatId, splitChatIds],
  );

  const streamingChatIdSet = useWorkingChatIds();

  const handleSelect = useCallback(
    (chatId: string) => {
      if (chatId !== routedChatId) {
        navigate(`/chat/${chatId}`);
        return;
      }
      // Re-clicking the current chat surfaces the agent pane.
      const ui = useUIStore.getState();
      if (ui.visibleLayout.flat().includes('agent:primary')) ui.focusTile('agent:primary');
      else ui.activateTab('agent:primary');
    },
    [navigate, routedChatId],
  );

  const handleClose = useCallback(
    (chatId: string) => {
      const ui = useUIStore.getState();
      // Closed tab leaves the working set — tear down its split too.
      if (ui.splitChatIds.includes(chatId)) ui.closeSplitChat(chatId);
      const tabs = ui.chatTabs;
      const index = tabs.indexOf(chatId);
      ui.closeChatTab(chatId);
      if (chatId !== routedChatId) return;
      const remaining = tabs.filter((id) => id !== chatId);
      const next = remaining[Math.min(index, remaining.length - 1)];
      navigate(next ? `/chat/${next}` : '/');
    },
    [navigate, routedChatId],
  );

  if (isMobile || chatTabs.length === 0) return null;

  return (
    <TitleBarTabs>
      {chatTabs.map((chatId) => {
        const status = chatTabStatus(
          chatId,
          blockedChatIdSet,
          streamingChatIdSet,
          completedChatIds,
        );
        return (
          <ChatTab
            key={chatId}
            chatId={chatId}
            isActive={visibleChatIds.has(chatId)}
            isCurrent={chatId === routedChatId}
            status={status}
            onSelect={handleSelect}
            onClose={handleClose}
          />
        );
      })}
    </TitleBarTabs>
  );
}
