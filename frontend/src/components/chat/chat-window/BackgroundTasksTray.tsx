import { memo, useEffect, useState } from 'react';
import clsx from 'clsx';
import { Bot, ChevronDown, Square, SquareTerminal } from 'lucide-react';
import toast from 'react-hot-toast';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { chatService } from '@/services/chatService';
import { useChatBackgroundTasks } from '@/store/backgroundTasksStore';
import type { BackgroundTask } from '@/types/chat.types';
import { formatElapsed } from '@/utils/backgroundTasks';
import styles from './BackgroundTasksTray.module.scss';

interface BackgroundTasksTrayProps {
  chatId: string;
  // A subagent runs inside the turn, so stopping it stops the turn.
  onStopTurn: () => void;
}

function useSecondTick(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);
  return now;
}

function TaskRow({
  chatId,
  task,
  now,
  onStopTurn,
}: {
  chatId: string;
  task: BackgroundTask;
  now: number;
  onStopTurn: () => void;
}) {
  const [stopping, setStopping] = useState(false);
  const Icon = task.kind === 'shell' ? SquareTerminal : Bot;

  const handleStop = async () => {
    if (!task.stoppable) {
      onStopTurn();
      return;
    }
    setStopping(true);
    try {
      await chatService.stopBackgroundTask(chatId, task.id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to stop the command');
      setStopping(false);
    }
  };

  return (
    <li className={styles.row}>
      <Icon className={styles['row-icon']} aria-hidden="true" />
      <FloatingTooltip content={task.title} className={styles['row-title-wrap']}>
        <span className={clsx(styles['row-title'], task.kind === 'shell' && styles.mono)}>
          {task.title}
        </span>
      </FloatingTooltip>
      <span className={styles.elapsed}>{formatElapsed(now - task.started_at * 1000)}</span>
      <FloatingTooltip content={task.stoppable ? 'Stop command' : 'Stop subagent (ends the turn)'}>
        <button
          type="button"
          className={styles['stop-btn']}
          onClick={() => void handleStop()}
          disabled={stopping}
          aria-label={`Stop ${task.title}`}
        >
          {stopping ? (
            <Spinner size="xs" />
          ) : (
            <Square className={styles['stop-glyph']} aria-hidden="true" />
          )}
        </button>
      </FloatingTooltip>
    </li>
  );
}

export const BackgroundTasksTray = memo(function BackgroundTasksTray({
  chatId,
  onStopTurn,
}: BackgroundTasksTrayProps) {
  const tasks = useChatBackgroundTasks(chatId);
  const [collapsed, setCollapsed] = useState(false);
  const now = useSecondTick(tasks.length > 0);

  if (tasks.length === 0) return null;

  const shells = tasks.filter((task) => task.kind === 'shell').length;
  const agents = tasks.length - shells;
  const summary = [
    shells > 0 && `${shells} ${shells === 1 ? 'command' : 'commands'}`,
    agents > 0 && `${agents} ${agents === 1 ? 'subagent' : 'subagents'}`,
  ]
    .filter(Boolean)
    .join(' · ');

  return (
    <div className={styles.tray}>
      <div className={styles.card}>
        <button
          type="button"
          className={styles.header}
          onClick={() => setCollapsed((value) => !value)}
          aria-expanded={!collapsed}
        >
          <span className={styles.pulse} aria-hidden="true" />
          <span className={styles['header-label']}>Running</span>
          <span className={styles['header-summary']}>{summary}</span>
          <ChevronDown
            className={clsx(styles.chevron, collapsed && styles['chevron--collapsed'])}
            aria-hidden="true"
          />
        </button>
        {!collapsed && (
          <ul className={styles.list}>
            {tasks.map((task) => (
              <TaskRow
                key={task.id}
                chatId={chatId}
                task={task}
                now={now}
                onStopTurn={onStopTurn}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
});
