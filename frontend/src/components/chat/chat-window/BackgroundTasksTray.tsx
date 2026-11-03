import { memo, useMemo, useState } from 'react';
import clsx from 'clsx';
import { Bot, ChevronDown, ChevronRight, Square, SquareTerminal } from 'lucide-react';
import toast from 'react-hot-toast';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { chatService } from '@/services/chatService';
import { useChatSubagentsQuery } from '@/hooks/queries/useSubagentQueries';
import { useSecondTick } from '@/hooks/useSecondTick';
import { useChatBackgroundTasks } from '@/store/backgroundTasksStore';
import { useUIStore } from '@/store/uiStore';
import type { BackgroundTask, Subagent } from '@/types/chat.types';
import { formatElapsed } from '@/utils/backgroundTasks';
import styles from './BackgroundTasksTray.module.scss';

interface BackgroundTasksTrayProps {
  chatId: string;
}

function TaskRow({ chatId, task, now }: { chatId: string; task: BackgroundTask; now: number }) {
  const [stopping, setStopping] = useState(false);
  const shell = task.kind === 'shell';
  const Icon = shell ? SquareTerminal : Bot;
  const noun = shell ? 'command' : 'task';

  const handleStop = async () => {
    setStopping(true);
    try {
      await chatService.stopBackgroundTask(chatId, task.id);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : `Failed to stop the ${noun}`);
      setStopping(false);
    }
  };

  return (
    <li className={styles.row}>
      <Icon className={styles['row-icon']} aria-hidden="true" />
      <FloatingTooltip content={task.title} className={styles['row-title-wrap']}>
        <span className={clsx(styles['row-title'], shell && styles.mono)}>{task.title}</span>
      </FloatingTooltip>
      <span className={styles.elapsed}>{formatElapsed(now - task.started_at * 1000)}</span>
      <FloatingTooltip content={`Stop ${noun}`}>
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

// A running subagent: its row opens that subagent's pane, where its details
// and stop button live. On a phone it's the only sign the thread is at work.
function SubagentRow({
  chatId,
  subagent,
  now,
}: {
  chatId: string;
  subagent: Subagent;
  now: number;
}) {
  const title = subagent.description || subagent.subagent_type;
  return (
    <li>
      <button
        type="button"
        className={clsx(styles.row, styles['row--button'])}
        onClick={() => useUIStore.getState().toggleSubagent(chatId, subagent.id)}
        aria-label={`Show subagent ${title}`}
      >
        <Bot className={styles['row-icon']} aria-hidden="true" />
        <span className={styles['row-title-wrap']}>
          <span className={styles['row-title']}>{title}</span>
          {subagent.last_activity && (
            <span className={styles['row-detail']}>{subagent.last_activity}</span>
          )}
        </span>
        <span className={styles.elapsed}>{formatElapsed(now - subagent.started_at * 1000)}</span>
        <ChevronRight className={styles['open-glyph']} aria-hidden="true" />
      </button>
    </li>
  );
}

function plural(count: number, noun: string): string | false {
  return count > 0 && `${count} ${noun}${count === 1 ? '' : 's'}`;
}

// Background work above the composer: each background task, and running
// subagents. A task that belongs to a tracked subagent is shown as that subagent.
export const BackgroundTasksTray = memo(function BackgroundTasksTray({
  chatId,
}: BackgroundTasksTrayProps) {
  const allTasks = useChatBackgroundTasks(chatId);
  const { data: allSubagents } = useChatSubagentsQuery(chatId);
  const tasks = useMemo(() => {
    const toolIds = new Set((allSubagents ?? []).map((s) => s.id));
    const taskIds = new Set((allSubagents ?? []).flatMap((s) => (s.task_id ? [s.task_id] : [])));
    return allTasks.filter((task) => !taskIds.has(task.id) && !toolIds.has(task.tool_id));
  }, [allTasks, allSubagents]);
  const subagents = useMemo(
    () => (allSubagents ?? []).filter((s) => s.status === 'running'),
    [allSubagents],
  );
  const [collapsed, setCollapsed] = useState(false);
  const now = useSecondTick(tasks.length > 0 || subagents.length > 0);

  if (tasks.length === 0 && subagents.length === 0) return null;

  const commands = tasks.filter((task) => task.kind === 'shell').length;
  const summary = [
    plural(commands, 'command'),
    plural(tasks.length - commands, 'task'),
    plural(subagents.length, 'subagent'),
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
              <TaskRow key={task.id} chatId={chatId} task={task} now={now} />
            ))}
            {subagents.map((subagent) => (
              <SubagentRow key={subagent.id} chatId={chatId} subagent={subagent} now={now} />
            ))}
          </ul>
        )}
      </div>
    </div>
  );
});
