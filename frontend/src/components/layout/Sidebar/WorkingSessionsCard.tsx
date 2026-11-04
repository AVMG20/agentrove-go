import { useEffect, useMemo, useState } from 'react';
import clsx from 'clsx';
import { Check, Zap } from 'lucide-react';
import type { Chat } from '@/types/chat.types';
import type { WorkspaceBadge } from '@/hooks/queries/useSidebarChatLists';
import { useChatQuery } from '@/hooks/queries/useChatQueries';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import { useNow } from '@/hooks/useNow';
import { useStreamStore } from '@/store/streamStore';
import { useBackgroundTasksStore } from '@/store/backgroundTasksStore';
import { AsciiSpinner } from '@/components/ui/AsciiSpinner/AsciiSpinner';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { formatElapsed } from '@/utils/backgroundTasks';
import { stripMarkdownTitle } from '@/utils/format';
import {
  MIN_WORKING_CARD_SESSIONS,
  orderSessions,
  reconcileSessions,
  type TrackedSession,
} from './workingSessions';
import styles from './WorkingSessionsCard.module.scss';

interface WorkingSessionsCardProps {
  chatsById: ReadonlyMap<string, Chat>;
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  workingChatIds: Set<string>;
  completedChatIds: Set<string>;
  blockedChatIds: Set<string>;
  onSelect: (chatId: string) => void;
}

// Floats over the bottom of the thread list while two or more sessions are
// running (or finished and not yet opened): what each is doing right now.
export function WorkingSessionsCard({
  chatsById,
  workspaceBadgeById,
  workingChatIds,
  completedChatIds,
  blockedChatIds,
  onSelect,
}: WorkingSessionsCardProps) {
  const activeStreamMetadata = useStreamStore((state) => state.activeStreamMetadata);
  const tasksByChat = useBackgroundTasksStore((state) => state.tasksByChat);
  // When each chat's work began: its turn, else its oldest background task.
  const startTimeByChat = useMemo(() => {
    const map = new Map<string, number>();
    for (const [chatId, tasks] of Object.entries(tasksByChat)) {
      if (tasks.length > 0) map.set(chatId, Math.min(...tasks.map((t) => t.started_at * 1000)));
    }
    for (const meta of activeStreamMetadata) map.set(meta.chatId, meta.startTime);
    return map;
  }, [activeStreamMetadata, tasksByChat]);

  const [sessions, setSessions] = useState<TrackedSession[]>([]);
  useEffect(() => {
    setSessions((prev) =>
      reconcileSessions(prev, workingChatIds, completedChatIds, startTimeByChat, Date.now()),
    );
  }, [workingChatIds, completedChatIds, startTimeByChat]);

  if (sessions.length < MIN_WORKING_CARD_SESSIONS) return null;
  return (
    <CardBody
      sessions={orderSessions(sessions)}
      chatsById={chatsById}
      workspaceBadgeById={workspaceBadgeById}
      blockedChatIds={blockedChatIds}
      onSelect={onSelect}
    />
  );
}

// Split out so the one-second timer only ticks while the card is on screen.
function CardBody({
  sessions,
  chatsById,
  workspaceBadgeById,
  blockedChatIds,
  onSelect,
}: {
  sessions: TrackedSession[];
  chatsById: ReadonlyMap<string, Chat>;
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  blockedChatIds: Set<string>;
  onSelect: (chatId: string) => void;
}) {
  const now = useNow(1000);
  const runningCount = sessions.filter((s) => s.endedAt === null).length;
  return (
    <section className={styles.card} aria-label="Working sessions">
      <header className={styles.header}>
        <span
          aria-hidden
          className={clsx(styles['header-dot'], runningCount === 0 && styles['header-dot--idle'])}
        />
        <span className={styles['header-label']}>Working</span>
        <span className={styles['header-count']}>{sessions.length}</span>
      </header>
      <ul className={styles.list}>
        {sessions.map((session) => (
          <li key={session.chatId}>
            <SessionEntry
              session={session}
              chat={chatsById.get(session.chatId)}
              workspaceBadgeById={workspaceBadgeById}
              isBlocked={blockedChatIds.has(session.chatId)}
              now={now}
              onSelect={onSelect}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}

function SessionEntry({
  session,
  chat: listedChat,
  workspaceBadgeById,
  isBlocked,
  now,
  onSelect,
}: {
  session: TrackedSession;
  chat: Chat | undefined;
  workspaceBadgeById: Map<string, WorkspaceBadge>;
  isBlocked: boolean;
  now: number;
  onSelect: (chatId: string) => void;
}) {
  const { chatId, startedAt, endedAt } = session;
  // Chats past the loaded sidebar pages are fetched on their own.
  const { data: fetchedChat } = useChatQuery(chatId, { enabled: !listedChat });
  const chat = listedChat ?? fetchedChat;
  const agentKind = useChatAgentKind(chatId, chat?.session_agent_kind, chat?.provider);
  const lastToolTitle = useStreamStore((state) => state.lastToolTitleByChatId.get(chatId));
  const backgroundTitle = useBackgroundTasksStore((state) => state.tasksByChat[chatId]?.[0]?.title);
  const isDone = endedAt !== null;
  const workspaceName = chat ? workspaceBadgeById.get(chat.workspace_id)?.name : undefined;

  return (
    <button type="button" className={styles.entry} onClick={() => onSelect(chatId)}>
      <span className={styles['entry-title-row']}>
        <span className={styles['entry-icon']}>
          {chat?.automation_id ? (
            <Zap aria-hidden className={clsx(styles.icon, styles['icon--automation'])} />
          ) : agentKind ? (
            <ProviderIcon provider={agentKind} className={styles.icon} />
          ) : null}
        </span>
        <span className={styles['entry-title']}>
          {chat ? stripMarkdownTitle(chat.title) : 'Thread'}
        </span>
      </span>
      <span className={styles['entry-detail']}>
        {isDone ? (
          <span className={clsx(styles.activity, styles['activity--done'])}>
            <Check aria-hidden className={styles['activity-icon']} />
            Done
          </span>
        ) : isBlocked ? (
          <span className={clsx(styles.activity, styles['activity--blocked'])}>
            <span aria-hidden className={styles['blocked-dot']} />
            Needs you
          </span>
        ) : (
          <span className={styles.activity}>
            <AsciiSpinner className={styles.spinner} />
            <span className={styles['activity-text']}>
              {lastToolTitle ?? backgroundTitle ?? 'Thinking'}
            </span>
          </span>
        )}
        <span className={styles.meta}>
          <span className={styles['meta-workspace']}>{workspaceName}</span>
          <span className={styles['meta-time']}>{formatElapsed((endedAt ?? now) - startedAt)}</span>
        </span>
      </span>
    </button>
  );
}
