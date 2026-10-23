import type { CustomSkill } from './user.types';
import type { AgentKind } from './chat.types';
import type { SlashCommand } from './ui.types';

export type WorkspaceSourceType = 'git' | 'empty';

export interface Workspace {
  id: string;
  name: string;
  user_id: string;
  sandbox_id: string;
  workspace_path: string;
  source_type?: string | null;
  source_url?: string | null;
  created_at: string;
  updated_at: string;
  chat_count: number;
  last_chat_at: string | null;
}

export interface CreateWorkspaceRequest {
  name: string;
  source_type: WorkspaceSourceType;
  git_url?: string;
  // Every project runs in the user's box.
  sandbox_provider: 'docker';
}

export interface UpdateWorkspaceRequest {
  name?: string;
}

export interface WorkspaceDiskUsage {
  workspace_id: string;
  files_bytes: number | null;
}

export type BoxState = 'running' | 'stopped' | 'none';

// The user's one Docker box, which holds every Docker project.
export interface BoxInfo {
  // Docker is reachable; false on host-only setups.
  available: boolean;
  state: BoxState;
  // A Claude turn is running, so it can't be stopped.
  busy: boolean;
  idle_for_seconds: number;
  idle_ttl_seconds: number;
  // What the box wrote outside home and projects (apt packages).
  disk_bytes: number | null;
}

export interface WorkspaceResources {
  skills: CustomSkill[];
  builtin_slash_commands: Record<AgentKind, SlashCommand[]>;
}
