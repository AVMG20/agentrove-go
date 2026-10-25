import { useRef, useState } from 'react';
import { useMatch } from 'react-router-dom';
import toast from 'react-hot-toast';
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
import { useChatQuery, useUpdateChatMutation } from '@/hooks/queries/useChatQueries';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import { useIsMobile } from '@/hooks/useIsMobile';
import { chatStatusTone, CHAT_STATUS_LABEL } from '@/utils/message';
import { stripMarkdownTitle } from '@/utils/format';
import styles from './ChatTitle.module.scss';

// Desktop title edited in place: Enter or leaving the field saves, Escape
// cancels.
function TitleEditor({
  chatId,
  initial,
  onDone,
}: {
  chatId: string;
  initial: string;
  onDone: () => void;
}) {
  const [value, setValue] = useState(initial);
  const updateChat = useUpdateChatMutation();
  // Escape closes without saving, and the blur that follows mustn't save.
  const finished = useRef(false);

  const save = () => {
    if (finished.current) return;
    finished.current = true;
    const title = value.trim();
    onDone();
    if (!title || title === initial) return;
    updateChat.mutate(
      { chatId, updateData: { title } },
      { onError: (error) => toast.error(error.message || 'Failed to rename thread') },
    );
  };

  return (
    <input
      className={styles['title-input']}
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onBlur={save}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          e.currentTarget.blur();
        } else if (e.key === 'Escape') {
          e.preventDefault();
          finished.current = true;
          onDone();
        }
      }}
      maxLength={255}
      aria-label="Thread title"
      autoFocus
      onFocus={(e) => e.target.select()}
    />
  );
}

// The routed thread's title and status. On phones it opens the session panel
// as a bottom sheet; on desktop the panel sits beside the chat and a double
// click renames the thread in place.
export function ChatTitle() {
  const chatId = useMatch('/chat/:chatId')?.params.chatId;
  const { data: chat } = useChatQuery(chatId);
  const completedChatIds = useStreamStore((s) => s.completedChatIds);
  const blockedChatIdSet = useBlockedChatIds();
  const streamingChatIdSet = useWorkingChatIds();
  const agentKind = useChatAgentKind(chatId ?? '', chat?.session_agent_kind, chat?.provider);
  const isMobile = useIsMobile();
  // The chat being edited, so switching threads drops a half-typed title.
  const [editingId, setEditingId] = useState<string | null>(null);
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
      <h1
        className={styles.title}
        title={editingId === chatId ? undefined : `${label}\nDouble-click to rename`}
        onDoubleClick={() => chat && setEditingId(chatId)}
      >
        {icon}
        {editingId === chatId && chat ? (
          <TitleEditor
            chatId={chatId}
            initial={stripMarkdownTitle(chat.title)}
            onDone={() => setEditingId(null)}
          />
        ) : (
          <span className={styles['title-text']}>{title}</span>
        )}
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
