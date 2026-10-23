import { Suspense, useCallback, useState } from 'react';
import { Clock, Github, Play, TriangleAlert, Zap } from 'lucide-react';
import toast from 'react-hot-toast';
import { ListManagementTab } from '@/components/ui/ListManagementTab/ListManagementTab';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Switch } from '@/components/ui/primitives/Switch/Switch';
import { useSettingsContext } from '@/hooks/useSettingsContext';
import {
  useAutomationsQuery,
  useCreateAutomationMutation,
  useUpdateAutomationMutation,
  useDeleteAutomationMutation,
  useRunAutomationMutation,
} from '@/hooks/queries/useAutomationQueries';
import {
  DEFAULT_PERMISSION_MODE,
  DEFAULT_THINKING_MODE,
  DEFAULT_WORKTREE,
  DEFAULT_PERSONA,
} from '@/store/chatSettingsStore';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import { buildAgentChatFields } from '@/utils/chatRequest';
import {
  buildCronExpression,
  describeCron,
  parseCronExpression,
  DEFAULT_SCHEDULE,
} from '@/utils/automationSchedule';
import {
  buildGithubPollCron,
  DEFAULT_GITHUB_POLL_MINUTES,
  describeGithubTrigger,
  parseGithubPollMinutes,
} from '@/utils/automationGithub';
import { lazyNamed } from '@/utils/lazyNamed';
import type {
  Automation,
  AutomationCreateRequest,
  AutomationForm,
  AutomationUpdateRequest,
} from '@/types/automation.types';
import styles from './AutomationsSettingsTab.module.scss';

const AutomationEditDialog = lazyNamed(
  () => import('@/components/settings/dialogs/AutomationEditDialog/AutomationEditDialog'),
  'AutomationEditDialog',
);

const NEXT_RUN_FORMAT: Intl.DateTimeFormatOptions = {
  month: 'short',
  day: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
};

function createDefaultForm(): AutomationForm {
  return {
    name: '',
    prompt: '',
    model_id: '',
    workspace_id: '',
    trigger_type: 'schedule',
    schedule: { ...DEFAULT_SCHEDULE },
    github_repo: '',
    github_events: ['pull_request_opened'],
    github_poll_minutes: DEFAULT_GITHUB_POLL_MINUTES,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    permission_mode: DEFAULT_PERMISSION_MODE,
    thinking_mode: DEFAULT_THINKING_MODE,
    worktree: DEFAULT_WORKTREE,
    persona_name: DEFAULT_PERSONA,
  };
}

function formFromAutomation(automation: Automation): AutomationForm {
  return {
    name: automation.name,
    prompt: automation.prompt,
    model_id: automation.model_id,
    workspace_id: automation.workspace_id,
    trigger_type: automation.trigger_type,
    schedule:
      automation.trigger_type === 'schedule'
        ? parseCronExpression(automation.cron_expression)
        : { ...DEFAULT_SCHEDULE },
    github_repo: automation.github_repo ?? '',
    github_events: automation.github_events ?? ['pull_request_opened'],
    github_poll_minutes: parseGithubPollMinutes(automation.cron_expression),
    // Keep the zone the schedule was authored in — editing from another
    // timezone must not silently shift the automation's wall-clock time.
    timezone: automation.timezone,
    permission_mode: automation.permission_mode,
    thinking_mode: automation.thinking_mode ?? '',
    worktree: automation.worktree,
    persona_name: automation.selected_persona_name,
  };
}

export function AutomationsSettingsTab() {
  const { localSettings } = useSettingsContext();

  const { data: automations } = useAutomationsQuery();
  const modelMap = useModelMap();

  const createMutation = useCreateAutomationMutation();
  const updateMutation = useUpdateAutomationMutation();
  const deleteMutation = useDeleteAutomationMutation();
  const runMutation = useRunAutomationMutation();

  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [editingAutomation, setEditingAutomation] = useState<Automation | null>(null);
  const [form, setForm] = useState<AutomationForm>(createDefaultForm);
  const [formError, setFormError] = useState<string | null>(null);

  const handleFormChange = useCallback(
    <K extends keyof AutomationForm>(field: K, value: AutomationForm[K]) => {
      setForm((prev) => ({ ...prev, [field]: value }));
    },
    [],
  );

  const handleAdd = () => {
    setEditingAutomation(null);
    setForm(createDefaultForm());
    setFormError(null);
    setIsDialogOpen(true);
  };

  const handleEdit = (index: number) => {
    const automation = automations?.[index];
    if (!automation) return;
    setEditingAutomation(automation);
    setForm(formFromAutomation(automation));
    setFormError(null);
    setIsDialogOpen(true);
  };

  const handleDelete = async (index: number) => {
    const automation = automations?.[index];
    if (!automation) return;
    await deleteMutation.mutateAsync(automation.id);
  };

  const handleSave = async () => {
    const personas = localSettings.personas ?? [];
    // Coerce raw form settings to the selected model's agent, same as normal
    // sends — a stale mode from another agent family would fail at run time.
    const agentFields = buildAgentChatFields(
      form.model_id,
      modelMap,
      {
        permissionMode: form.permission_mode,
        thinkingMode: form.thinking_mode,
        worktree: form.worktree,
        persona: form.persona_name,
      },
      personas,
    );
    const isGithub = form.trigger_type === 'github';
    const data: AutomationCreateRequest = {
      name: form.name.trim(),
      prompt: form.prompt,
      model_id: form.model_id,
      workspace_id: form.workspace_id,
      trigger_type: form.trigger_type,
      github_repo: isGithub ? form.github_repo.trim() : null,
      github_events: isGithub ? form.github_events : null,
      cron_expression: isGithub
        ? buildGithubPollCron(form.github_poll_minutes)
        : buildCronExpression(form.schedule),
      timezone: form.timezone,
      permission_mode: agentFields.permission_mode,
      thinking_mode: agentFields.thinking_mode || null,
      worktree: agentFields.worktree ?? false,
      selected_persona_name: agentFields.selected_persona_name,
      enabled: editingAutomation?.enabled ?? true,
    };
    try {
      if (editingAutomation) {
        await updateMutation.mutateAsync({ automationId: editingAutomation.id, data });
      } else {
        await createMutation.mutateAsync(data);
      }
      setIsDialogOpen(false);
      toast.success('Saved', { id: 'automation-saved' });
    } catch (error) {
      setFormError(error instanceof Error ? error.message : 'Failed to save automation');
    }
  };

  const handleToggle = async (automation: Automation, enabled: boolean) => {
    const patch: AutomationUpdateRequest = { enabled };
    try {
      await updateMutation.mutateAsync({ automationId: automation.id, data: patch });
    } catch {
      toast.error('Failed to update automation');
    }
  };

  const handleRunNow = async (automation: Automation) => {
    try {
      await runMutation.mutateAsync(automation.id);
      toast.success(`"${automation.name}" started — check the sidebar for its chat`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to run automation');
    }
  };

  return (
    <>
      <ListManagementTab<Automation>
        title="Automations"
        description="Prompts that start a new chat with your chosen model, workspace, and settings — on a schedule (e.g. check email daily) or when something happens on GitHub (e.g. review every new PR)."
        items={automations ?? null}
        emptyIcon={Clock}
        emptyText="No automations configured yet"
        emptyButtonText="Add your first automation"
        addButtonText="Add automation"
        deleteConfirmTitle="Delete automation"
        deleteConfirmMessage={(automation) =>
          `Are you sure you want to delete "${automation.name}"? This action cannot be undone.`
        }
        getItemKey={(automation) => automation.id}
        onAdd={handleAdd}
        onEdit={handleEdit}
        onDelete={handleDelete}
        renderItem={(automation) => (
          <>
            <div className={styles['item-header']}>
              <h3 className={styles['item-title']}>{automation.name}</h3>
            </div>
            <div className={styles['item-meta']}>
              <span className={styles.chip}>{automation.model_id}</span>
              {automation.trigger_type === 'github' ? (
                <span className={styles.chip}>
                  <Github className={styles['chip-icon']} />
                  {describeGithubTrigger(automation.github_repo, automation.github_events)}
                </span>
              ) : (
                <span className={styles.chip}>{describeCron(automation.cron_expression)}</span>
              )}
              {automation.trigger_type === 'github' &&
                (automation.github_webhook_active ? (
                  <span className={styles.chip} title="GitHub delivers new items via a webhook">
                    <Zap className={styles['chip-icon']} />
                    Instant
                  </span>
                ) : (
                  <span
                    className={styles.chip}
                    title={
                      automation.github_webhook_error
                        ? `No webhook: ${automation.github_webhook_error}`
                        : 'No webhook; checking on a schedule'
                    }
                  >
                    <TriangleAlert className={styles['chip-icon']} />
                    Polling
                  </span>
                ))}
              {automation.enabled && !automation.github_webhook_active && (
                <span className={styles['next-run']}>
                  {automation.trigger_type === 'github' ? 'Next check' : 'Next run'}{' '}
                  {new Date(automation.next_run_at).toLocaleString([], NEXT_RUN_FORMAT)}
                </span>
              )}
            </div>
          </>
        )}
        renderItemActions={(automation) => (
          <>
            <Button
              variant="unstyled"
              onClick={() => void handleRunNow(automation)}
              disabled={runMutation.isPending}
              className={styles['run-button']}
              aria-label="Run now"
              title={
                automation.trigger_type === 'github'
                  ? 'Run now with the latest matching GitHub event'
                  : 'Run now'
              }
            >
              <Play className={styles['run-icon']} />
            </Button>
            <Switch
              size="sm"
              className={styles.toggle}
              checked={automation.enabled}
              onCheckedChange={(enabled) => void handleToggle(automation, enabled)}
              aria-label={automation.enabled ? 'Disable automation' : 'Enable automation'}
            />
          </>
        )}
        logContext="AutomationsSettingsTab"
      />
      {isDialogOpen && (
        <Suspense fallback={null}>
          <AutomationEditDialog
            isOpen={isDialogOpen}
            isEditing={editingAutomation !== null}
            form={form}
            personas={localSettings.personas ?? []}
            error={formError}
            isSaving={createMutation.isPending || updateMutation.isPending}
            onClose={() => setIsDialogOpen(false)}
            onSubmit={() => void handleSave()}
            onChange={handleFormChange}
          />
        </Suspense>
      )}
    </>
  );
}
