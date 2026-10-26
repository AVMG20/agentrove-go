import type { Subagent, SubagentStatus } from '@/types/chat.types';
import { formatElapsed } from '@/utils/backgroundTasks';

export const SUBAGENT_STATUS_LABEL: Record<SubagentStatus, string> = {
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  stopped: 'Stopped',
};

export function subagentStatusLabel(subagent: Subagent): string {
  if (subagent.status === 'running' && subagent.background) return 'Running in background';
  return SUBAGENT_STATUS_LABEL[subagent.status];
}

// Live while running, then how long it ran.
export function subagentDuration(subagent: Subagent, now: number): string {
  const end = subagent.finished_at != null ? subagent.finished_at * 1000 : now;
  return formatElapsed(end - subagent.started_at * 1000);
}
