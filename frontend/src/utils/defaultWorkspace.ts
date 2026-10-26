import type { Chat } from '@/types/chat.types';
import type { Workspace } from '@/types/workspace.types';

// New threads start where the latest one ran: the most recently active project
// (the list's order), or no project (null) when a thread without one is newer.
export function defaultWorkspaceId(workspaces: Workspace[], recentChats: Chat[]): string | null {
  const project = workspaces[0];
  const latest = recentChats[0];
  if (!project) return null;
  if (
    latest?.no_project &&
    (!project.last_chat_at || Date.parse(latest.updated_at) >= Date.parse(project.last_chat_at))
  ) {
    return null;
  }
  return project.id;
}
