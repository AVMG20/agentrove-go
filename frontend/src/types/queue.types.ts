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
}
