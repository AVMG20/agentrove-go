import type { PermissionMode } from '@/store/chatSettingsStore';
import type { ScheduleForm } from '@/utils/automationSchedule';

export type AutomationTriggerType = 'schedule' | 'github';

export type GitHubTriggerEvent =
  | 'pull_request_opened'
  | 'issue_opened'
  | 'issue_comment_created'
  | 'release_published';

export interface Automation {
  id: string;
  user_id: string;
  workspace_id: string;
  name: string;
  prompt: string;
  model_id: string;
  cron_expression: string;
  timezone: string;
  permission_mode: PermissionMode;
  thinking_mode: string | null;
  worktree: boolean;
  selected_persona_name: string;
  enabled: boolean;
  next_run_at: string;
  last_run_at: string | null;
  // 'github' polls github_repo on cron_expression and runs once per new event.
  trigger_type: AutomationTriggerType;
  github_repo: string | null;
  github_events: GitHubTriggerEvent[] | null;
  // A repo webhook delivers events instantly; otherwise the cron poll runs.
  github_webhook_active: boolean;
  github_webhook_error: string | null;
  created_at: string;
  updated_at: string;
}

export interface AutomationCreateRequest {
  name: string;
  prompt: string;
  model_id: string;
  workspace_id: string;
  cron_expression: string;
  timezone: string;
  permission_mode: PermissionMode;
  thinking_mode: string | null;
  worktree: boolean;
  selected_persona_name: string;
  enabled: boolean;
  trigger_type: AutomationTriggerType;
  github_repo: string | null;
  github_events: GitHubTriggerEvent[] | null;
}

export type AutomationUpdateRequest = Partial<AutomationCreateRequest>;

// Inputbar settings an automation replays; shared edit-dialog form shape.
export interface AutomationForm {
  name: string;
  prompt: string;
  model_id: string;
  workspace_id: string;
  trigger_type: AutomationTriggerType;
  schedule: ScheduleForm;
  github_repo: string;
  github_events: GitHubTriggerEvent[];
  github_poll_minutes: number;
  timezone: string;
  permission_mode: PermissionMode;
  thinking_mode: string;
  worktree: boolean;
  persona_name: string;
}
