import { useMatch } from 'react-router-dom';
import { ChevronDown, Zap } from 'lucide-react';
import clsx from 'clsx';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { ChatStatusDot } from '@/components/ui/ChatStatusDot/ChatStatusDot';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useUIStore } from '@/store/uiStore';
import { useStreamStore } from '@/store/streamStore';
import { useBlockedChatIds } from '@/hooks/useBlockedChatIds';
import { useWorkingChatIds } from '@/hooks/useWorkingChatIds';
import { useChatQuery } from '@/hooks/queries/useChatQueries';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import { useIsMobile } from '@/hooks/useIsMobile';
import { chatStatusTone, CHAT_STATUS_LABEL } from '@/utils/message';
import { stripMarkdownTitle } from '@/utils/format';
import styles from './ChatTitle.module.scss';

// The routed thread's title and status. On phones it opens the session panel
// as a bottom sheet; on desktop the panel sits beside the chat.
export function ChatTitle() {
  const chatId = useMatch('/chat/:chatId')?.params.chatId;
  const { data: chat } = useChatQuery(chatId);
  const completedChatIds = useStreamStore((s) => s.completedChatIds);
  const blockedChatIdSet = useBlockedChatIds();
  const streamingChatIdSet = useWorkingChatIds();
  const agentKind = useChatAgentKind(chatId ?? '', chat?.session_agent_kind, chat?.provider);
  const isMobile = useIsMobile();
  if (!chatId) return null;

  const status = chatStatusTone({
    blocked: blockedChatIdSet.has(chatId),
    streaming: streamingChatIdSet.has(chatId),
    completed: completedChatIds.has(chatId),
  });
  const title = chat?.title ? stripMarkdownTitle(chat.title) : '…';
  const icon =
    status === 'running' ? (
      <AsciiSpinner className={styles.spinner} />
    ) : status ? (
      <ChatStatusDot tone={status} className={styles['status-dot']} />
    ) : chat?.automation_id ? (
      <Zap aria-hidden className={clsx(styles['provider-icon'], styles['automation-icon'])} />
    ) : (
      agentKind && <ProviderIcon provider={agentKind} className={styles['provider-icon']} />
    );
  const label = `${title}${status ? ` · ${CHAT_STATUS_LABEL[status]}` : ''}`;

  if (!isMobile) {
    return (
      <h1 className={styles.title} title={label}>
        {icon}
        <span className={styles['title-text']}>{title}</span>
      </h1>
    );
  }
  return (
    <Button
      type="button"
      variant="unstyled"
      className={clsx(styles.title, styles['title--button'])}
      onClick={() => useUIStore.getState().setSessionSheetOpen(true)}
      aria-label={`${label}. Show session details`}
    >
      {icon}
      <span className={styles['title-text']}>{title}</span>
      <ChevronDown aria-hidden className={styles.chevron} />
    </Button>
  );
}
