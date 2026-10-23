import { useEffect, useRef } from 'react';
import { Clock, Brain, Shield, GitFork, Github } from 'lucide-react';
import type { Persona } from '@/types/user.types';
import type { AutomationForm, AutomationTriggerType } from '@/types/automation.types';
import { Input } from '@/components/ui/primitives/Input/Input';
import { Label } from '@/components/ui/primitives/Label/Label';
import { Select } from '@/components/ui/primitives/Select/Select';
import { Textarea } from '@/components/ui/primitives/Textarea/Textarea';
import { Dropdown } from '@/components/ui/primitives/Dropdown/Dropdown';
import { SegmentedControl } from '@/components/ui/primitives/SegmentedControl/SegmentedControl';
import { BaseModal } from '@/components/ui/shared/BaseModal/BaseModal';
import { DialogFooter } from '@/components/ui/shared/DialogFooter/DialogFooter';
import { DialogError } from '@/components/ui/shared/DialogError/DialogError';
import {
  ToggleDropdown,
  type ToggleDropdownOption,
} from '@/components/ui/shared/ToggleDropdown/ToggleDropdown';
import { ModelSelector } from '@/components/chat/model-selector/ModelSelector';
import {
  getThinkingModes,
  getThinkingModeOption,
} from '@/components/chat/thinking-mode-selector/thinkingModes';
import {
  PERMISSION_MODES,
  getPermissionModeOption,
} from '@/components/chat/permission-mode-selector/permissionModes';
import { useModelsQuery } from '@/hooks/queries/useModelQueries';
import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import { DEFAULT_PERSONA } from '@/store/chatSettingsStore';
import {
  buildCronExpression,
  describeCron,
  WEEKDAY_LABELS,
  type ScheduleForm,
  type ScheduleFrequency,
} from '@/utils/automationSchedule';
import { GITHUB_EVENT_OPTIONS, GITHUB_POLL_OPTIONS } from '@/utils/automationGithub';
import { GitHubRepoPicker } from './GitHubRepoPicker';
import styles from './AutomationEditDialog.module.scss';

const TRIGGER_OPTIONS: { value: AutomationTriggerType; label: string }[] = [
  { value: 'schedule', label: 'Schedule' },
  { value: 'github', label: 'GitHub event' },
];

const GITHUB_REPO_PATTERN = /^[A-Za-z0-9-]+\/[A-Za-z0-9._-]+$/;

const WORKTREE_OPTIONS: readonly [ToggleDropdownOption, ToggleDropdownOption] = [
  { label: 'No worktree' },
  { label: 'Worktree' },
];

interface AutomationEditDialogProps {
  isOpen: boolean;
  isEditing: boolean;
  form: AutomationForm;
  personas: Persona[];
  error: string | null;
  isSaving: boolean;
  onClose: () => void;
  onSubmit: () => void;
  onChange: <K extends keyof AutomationForm>(field: K, value: AutomationForm[K]) => void;
}

export function AutomationEditDialog({
  isOpen,
  isEditing,
  form,
  personas,
  error,
  isSaving,
  onClose,
  onSubmit,
  onChange,
}: AutomationEditDialogProps) {
  const { data: models = [] } = useModelsQuery();
  const workspaces = useWorkspacesList();

  // Seed model when missing/left registry. Effect (onChange writes parent state).
  useEffect(() => {
    if (models.length > 0 && !models.some((m) => m.model_id === form.model_id)) {
      onChange('model_id', models[0].model_id);
    }
  }, [models, form.model_id, onChange]);

  // A missing workspace id means it was deleted; re-seed.
  useEffect(() => {
    if (workspaces.length > 0 && !workspaces.some((w) => w.id === form.workspace_id)) {
      onChange('workspace_id', workspaces[0].id);
    }
  }, [workspaces, form.workspace_id, onChange]);

  // New GitHub automations default to a worktree: several PRs can run at
  // once in one project and would otherwise fight over its checkout. Only
  // until the user picks a worktree setting themselves.
  const worktreeTouched = useRef(false);
  useEffect(() => {
    if (isOpen) worktreeTouched.current = false;
  }, [isOpen]);
  const changeTrigger = (value: AutomationTriggerType) => {
    onChange('trigger_type', value);
    if (!isEditing && !worktreeTouched.current) onChange('worktree', value === 'github');
  };

  const selectedModel = models.find((m) => m.model_id === form.model_id);

  const selectedPermissionOption = getPermissionModeOption(form.permission_mode);
  const thinkingModes = getThinkingModes(selectedModel);
  const selectedThinkingOption = getThinkingModeOption(form.thinking_mode, selectedModel);

  const { schedule } = form;
  const setSchedule = (patch: Partial<ScheduleForm>) =>
    onChange('schedule', { ...schedule, ...patch });
  const cronPreview = buildCronExpression(schedule);
  const isGithub = form.trigger_type === 'github';
  const githubRepo = form.github_repo.trim().replace(/^https:\/\/github\.com\//, '');
  const githubRepoValid = GITHUB_REPO_PATTERN.test(githubRepo.replace(/\/$/, ''));

  const toggleGithubEvent = (event: (typeof GITHUB_EVENT_OPTIONS)[number]['value']) =>
    onChange(
      'github_events',
      form.github_events.includes(event)
        ? form.github_events.filter((e) => e !== event)
        : [...form.github_events, event],
    );

  const triggerValid = isGithub
    ? githubRepoValid && form.github_events.length > 0
    : schedule.frequency !== 'custom' || !!schedule.cron.trim();

  const canSave =
    !!form.name.trim() &&
    !!form.prompt.trim() &&
    !!form.model_id &&
    !!form.workspace_id &&
    triggerValid;

  return (
    <BaseModal isOpen={isOpen} onClose={onClose} size="2xl" className={styles.dialog}>
      <div className={styles.body}>
        <div className={styles.header}>
          <div className={styles['icon-box']}>
            {isGithub ? (
              <Github className={styles['header-icon']} />
            ) : (
              <Clock className={styles['header-icon']} />
            )}
          </div>
          <h3 className={styles.title}>{isEditing ? 'Edit automation' : 'Add automation'}</h3>
        </div>

        <DialogError error={error} className={styles.error} />

        <div className={styles.fields}>
          <div>
            <Label className={styles.label}>Name</Label>
            <Input
              value={form.name}
              onChange={(e) => onChange('name', e.target.value)}
              placeholder="Check Gmail"
              className={styles.input}
            />
          </div>

          <div>
            <Label className={styles.label}>Project</Label>
            <div className={styles.row}>
              <Select
                value={form.workspace_id}
                onChange={(e) => onChange('workspace_id', e.target.value)}
                className={styles.select}
              >
                {workspaces.map((w) => (
                  <option key={w.id} value={w.id}>
                    {w.name}
                  </option>
                ))}
              </Select>
            </div>
            {workspaces.length === 0 && <p className={styles.hint}>No projects yet.</p>}
          </div>

          <div className={styles.columns}>
            <div>
              <Label className={styles.label}>Model</Label>
              <ModelSelector
                selectedModelId={form.model_id}
                onModelChange={(modelId) => onChange('model_id', modelId)}
                dropdownPosition="bottom"
                compact={false}
              />
            </div>

            <div>
              <Label className={styles.label}>Persona</Label>
              <Select
                value={form.persona_name}
                onChange={(e) => onChange('persona_name', e.target.value)}
                className={styles.select}
              >
                <option value={DEFAULT_PERSONA}>Default</option>
                {personas.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          <div className={styles['control-row']}>
            {selectedThinkingOption && (
              <Dropdown
                value={selectedThinkingOption}
                items={thinkingModes}
                getItemKey={(m) => m.value}
                getItemLabel={(m) => m.label}
                onSelect={(m) => onChange('thinking_mode', m.value)}
                leftIcon={Brain}
                dropdownPosition="bottom"
              />
            )}
            <Dropdown
              value={selectedPermissionOption}
              items={PERMISSION_MODES}
              getItemKey={(m) => m.value}
              getItemLabel={(m) => m.label}
              onSelect={(m) => onChange('permission_mode', m.value)}
              leftIcon={Shield}
              dropdownPosition="bottom"
            />
            <ToggleDropdown
              options={WORKTREE_OPTIONS}
              value={form.worktree}
              onSelect={(enabled) => {
                worktreeTouched.current = true;
                onChange('worktree', enabled);
              }}
              icon={GitFork}
              width="9rem"
            />
          </div>
          {isGithub && (
            <p className={styles.hint}>
              {form.worktree
                ? 'Each run gets its own worktree, so several PRs can be handled at once in this project.'
                : 'Without a worktree, runs for different PRs share one checkout and can get in each other’s way.'}
            </p>
          )}

          <div>
            <Label className={styles.label}>Trigger</Label>
            <SegmentedControl
              options={TRIGGER_OPTIONS}
              value={form.trigger_type}
              onChange={changeTrigger}
              size="sm"
              className={styles.trigger}
            />
          </div>

          {isGithub ? (
            <div className={styles['github-fields']}>
              <div>
                <Label className={styles.label}>Repository</Label>
                <GitHubRepoPicker
                  value={form.github_repo}
                  onChange={(repo) => onChange('github_repo', repo)}
                  hasError={!!form.github_repo.trim() && !githubRepoValid}
                />
              </div>
              <div>
                <Label className={styles.label}>Run when</Label>
                <div className={styles['event-list']}>
                  {GITHUB_EVENT_OPTIONS.map((option) => (
                    <label key={option.value} className={styles['event-option']}>
                      <input
                        type="checkbox"
                        checked={form.github_events.includes(option.value)}
                        onChange={() => toggleGithubEvent(option.value)}
                        className={styles['event-checkbox']}
                      />
                      {option.label}
                    </label>
                  ))}
                </div>
              </div>
              <div className={styles['inline-field']}>
                Without a webhook, check every
                <div className={styles['select-w28']}>
                  <Select
                    value={form.github_poll_minutes}
                    onChange={(e) => onChange('github_poll_minutes', Number(e.target.value))}
                    className={styles.select}
                  >
                    {GITHUB_POLL_OPTIONS.map((minutes) => (
                      <option key={minutes} value={minutes}>
                        {minutes === 60 ? '1 hour' : `${minutes} min`}
                      </option>
                    ))}
                  </Select>
                </div>
              </div>
              <p className={styles.hint}>
                Uses the GitHub token from Settings → General. New items arrive instantly through a
                repository webhook when the token can manage webhooks (admin:repo_hook, or Webhooks:
                write for fine-grained tokens); otherwise the repo is checked on the interval above.
                Each item starts its own chat with its details appended to the prompt, and the
                agent&apos;s gh CLI acts as you (e.g. to comment). Your own activity is ignored so
                replies can&apos;t re-trigger the automation.
              </p>
            </div>
          ) : (
            <div>
              <Label className={styles.label}>Schedule</Label>
              <div className={styles['control-row']}>
                {/* Width on a wrapper, not the Select — its chevron is positioned
                  against the primitive's own w-full container */}
                <div className={styles['select-w36']}>
                  <Select
                    value={schedule.frequency}
                    onChange={(e) =>
                      setSchedule({ frequency: e.target.value as ScheduleFrequency })
                    }
                    className={styles.select}
                  >
                    <option value="hourly">Hourly interval</option>
                    <option value="daily">Daily</option>
                    <option value="weekly">Weekly</option>
                    <option value="monthly">Monthly</option>
                    <option value="custom">Custom (cron)</option>
                  </Select>
                </div>
                {schedule.frequency === 'hourly' && (
                  <div className={styles['inline-field']}>
                    every
                    <Input
                      type="number"
                      min={1}
                      max={23}
                      value={schedule.everyHours}
                      onChange={(e) =>
                        // Cron's hour field only spans 0-23 — a */N step ≥ 24 silently
                        // collapses to "daily at midnight", so clamp below that.
                        setSchedule({
                          everyHours: Math.min(23, Math.max(1, Number(e.target.value) || 1)),
                        })
                      }
                      className={styles['num-input']}
                    />
                    hours
                  </div>
                )}
                {schedule.frequency === 'weekly' && (
                  <div className={styles['select-w32']}>
                    <Select
                      value={schedule.dayOfWeek}
                      onChange={(e) => setSchedule({ dayOfWeek: Number(e.target.value) })}
                      className={styles.select}
                    >
                      {WEEKDAY_LABELS.map((label, day) => (
                        <option key={label} value={day}>
                          {label}
                        </option>
                      ))}
                    </Select>
                  </div>
                )}
                {schedule.frequency === 'monthly' && (
                  <div className={styles['inline-field']}>
                    day
                    <Input
                      type="number"
                      min={1}
                      max={31}
                      value={schedule.dayOfMonth}
                      onChange={(e) => setSchedule({ dayOfMonth: Number(e.target.value) || 1 })}
                      className={styles['num-input']}
                    />
                  </div>
                )}
                {(schedule.frequency === 'daily' ||
                  schedule.frequency === 'weekly' ||
                  schedule.frequency === 'monthly') && (
                  <div className={styles['inline-field']}>
                    at
                    <Input
                      type="time"
                      value={schedule.time}
                      onChange={(e) => setSchedule({ time: e.target.value })}
                      className={styles['time-input']}
                    />
                  </div>
                )}
                {schedule.frequency === 'custom' && (
                  <Input
                    value={schedule.cron}
                    onChange={(e) => setSchedule({ cron: e.target.value })}
                    placeholder="0 */6 * * *"
                    className={styles['cron-input']}
                  />
                )}
              </div>
              <p className={styles.hint}>
                {describeCron(cronPreview)} · {form.timezone}
              </p>
            </div>
          )}

          <div>
            <Label className={styles.label}>Prompt</Label>
            <Textarea
              value={form.prompt}
              onChange={(e) => onChange('prompt', e.target.value)}
              placeholder={
                isGithub
                  ? 'Review this pull request and leave a short summary of risks as a PR comment.'
                  : 'Check my Gmail inbox and summarize anything that needs a reply.'
              }
              className={styles['prompt-textarea']}
              rows={5}
            />
            <p className={styles.hint}>
              {isGithub
                ? 'Each event starts a new chat in the selected project with this prompt plus the event.'
                : 'Each run starts a new chat in the selected project with this prompt.'}
            </p>
          </div>
        </div>

        <DialogFooter
          onCancel={onClose}
          onSave={onSubmit}
          saveLabel={isEditing ? 'Update' : 'Add automation'}
          disabled={!canSave || isSaving}
        />
      </div>
    </BaseModal>
  );
}
