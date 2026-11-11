import type { BoxInfo, PruneBoxResult, Workspace } from '@/types/workspace.types';
import { formatBytes } from '@/utils/format';

// Paths inside the user's box (see backend/internal/sandbox).
export const BOX_HOME = '/home/user';
export const BOX_PROJECTS = `${BOX_HOME}/projects`;

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  return trimmed.slice(trimmed.lastIndexOf('/') + 1);
}

// Where the agent works: the project folder in the box, plus the chat's
// worktree when it has one. workspace_path is the server-side folder that is
// mounted there.
export function projectPath(workspace: Workspace, worktreeCwd?: string | null): string {
  const root = `${BOX_PROJECTS}/${basename(workspace.workspace_path)}`;
  return worktreeCwd ? `${root}/${worktreeCwd}` : root;
}

// A box path as ~/… for display, where space is short.
export function tildePath(path: string): string {
  return path === BOX_HOME || path.startsWith(`${BOX_HOME}/`)
    ? `~${path.slice(BOX_HOME.length)}`
    : path;
}

export function formatIdleDuration(seconds: number): string {
  const minutes = Math.ceil(Math.max(0, seconds) / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest ? `${hours}h ${rest}m` : `${hours}h`;
}

export type BoxTone = 'running' | 'stopped';

export interface BoxSummary {
  tone: BoxTone;
  // "Box running", "Box stopped"
  label: string;
  // "idles in 12m", "Claude working", "starts when you chat"
  detail: string;
}

// elapsedSeconds: time since `box` was fetched, so the countdown keeps moving
// between polls.
export function summarizeBox(box: BoxInfo, elapsedSeconds = 0): BoxSummary {
  if (box.state !== 'running') {
    return { tone: 'stopped', label: 'Box stopped', detail: 'starts when you chat' };
  }
  if (box.busy) return { tone: 'running', label: 'Box running', detail: 'Claude working' };
  // Idle stops turned off on the server.
  if (box.idle_ttl_seconds <= 0) return { tone: 'running', label: 'Box running', detail: 'ready' };
  const left = box.idle_ttl_seconds - box.idle_for_seconds - elapsedSeconds;
  return {
    tone: 'running',
    label: 'Box running',
    detail: left > 0 ? `idles in ${formatIdleDuration(left)}` : 'stopping soon',
  };
}

export function pruneSummary(result: PruneBoxResult): string {
  const lines = [
    result.freed_bytes != null ? `Freed ${formatBytes(result.freed_bytes)}.` : 'Box pruned.',
  ];
  if (result.removed.length > 0) {
    const n = result.removed.length;
    lines.push(
      `${n} ${n === 1 ? 'project is' : 'projects are'} cloned again when you next use them.`,
    );
  }
  for (const kept of result.kept) {
    lines.push(`Kept ${kept.name}: ${kept.reason}.`);
  }
  return lines.join('\n');
}
