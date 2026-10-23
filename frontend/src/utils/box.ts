import type { BoxInfo, Workspace } from '@/types/workspace.types';

// Paths inside the user's box (see backend/internal/sandbox).
export const BOX_HOME = '/home/user';
export const BOX_PROJECTS = `${BOX_HOME}/projects`;

function basename(path: string): string {
  const trimmed = path.replace(/\/+$/, '');
  return trimmed.slice(trimmed.lastIndexOf('/') + 1);
}

// Where the agent works: the project folder in the box (or on this computer
// for host projects), plus the chat's worktree when it has one.
export function projectPath(workspace: Workspace, worktreeCwd?: string | null): string {
  const root =
    workspace.sandbox_provider === 'docker'
      ? `${BOX_PROJECTS}/${basename(workspace.workspace_path)}`
      : workspace.workspace_path;
  return worktreeCwd ? `${root}/${worktreeCwd}` : root;
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
