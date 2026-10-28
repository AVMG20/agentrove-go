import type { PermissionMode } from '@/store/chatSettingsStore';

export interface QueueMessageAttachment {
  file_url: string;
  file_path?: string;
  file_type: string;
  filename?: string;
}

// Named options for queueMessage (avoids a growing positional boolean list).
export interface QueueMessageOptions {
  permissionMode?: PermissionMode;
  thinkingMode?: string | null;
  worktree?: boolean;
  baseBranch?: string;
  selectedPersonaName?: string;
  files?: File[];
}

export interface QueuedItem {
  id: string;
  content: string;
  queued_at: string;
  attachments?: QueueMessageAttachment[];
}

export interface QueuedMessage extends QueuedItem {
  model_id: string;
  permission_mode: PermissionMode;
  thinking_mode?: string | null;
  worktree: boolean;
  base_branch?: string | null;
  selected_persona_name: string;
}

export interface QueueAddResponse {
  id: string;
  // Handed straight to the running turn instead of queued.
  steered?: boolean;
}

export interface LocalQueuedMessage {
  id: string;
  content: string;
  files?: File[];
  attachments?: QueueMessageAttachment[];
  queuedAt: number;
  synced: boolean;
  // Never reached the server (network error); retried by hand or on reconnect.
  failed?: boolean;
  sendingNow: boolean;
  // Sent into the running turn; Claude takes it at its next step (running
  // subagents keep going), and it moves into the chat then (user_steering).
  awaitingPickup?: boolean;
  // Send now was pressed on it: the turn backgrounds its running tools and
  // subagents and stops to take it now.
  forced?: boolean;
}
