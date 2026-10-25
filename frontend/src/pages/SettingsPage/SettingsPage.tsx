import { useState, useCallback, useEffect, useRef, Suspense, type ReactNode } from 'react';
import { Navigate, useParams } from 'react-router-dom';
import type { UserSettings, UserSettingsUpdate } from '@/types/user.types';
import type { ApiFieldKey } from '@/types/settings.types';
import { useDeleteAllChatsMutation } from '@/hooks/queries/useChatQueries';
import { useSettingsQuery, useUpdateSettingsMutation } from '@/hooks/queries/useSettingsQueries';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { ErrorBoundary } from '@/components/ui/ErrorBoundary/ErrorBoundary';
import toast from 'react-hot-toast';
import { GeneralSettingsTab } from '@/components/settings/tabs/GeneralSettingsTab/GeneralSettingsTab';
import { SkillsSettingsTab } from '@/components/settings/tabs/SkillsSettingsTab/SkillsSettingsTab';
import { SettingsProvider } from '@/contexts/SettingsContext';
import { ChatProvider } from '@/contexts/ChatContext';
import { CommandMenu } from '@/components/ui/command-menu/CommandMenu';
import { useCommandMenu } from '@/hooks/useCommandMenu';
import { getGeneralSecretFields } from '@/utils/settings';
import { lazyNamed } from '@/utils/lazyNamed';
import {
  findSettingsSection,
  settingsPath,
  type SettingsSectionId,
} from '@/components/settings/settingsSections';
import styles from './SettingsPage.module.scss';

const PersonasSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/PersonasSettingsTab/PersonasSettingsTab'),
  'PersonasSettingsTab',
);
const StreamActionsSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/StreamActionsSettingsTab/StreamActionsSettingsTab'),
  'StreamActionsSettingsTab',
);
const AutomationsSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/AutomationsSettingsTab/AutomationsSettingsTab'),
  'AutomationsSettingsTab',
);
const ArtifactsSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/ArtifactsSettingsTab/ArtifactsSettingsTab'),
  'ArtifactsSettingsTab',
);
const WorkspacesSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/WorkspacesSettingsTab/WorkspacesSettingsTab'),
  'WorkspacesSettingsTab',
);
const ProvidersSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/ProvidersSettingsTab/ProvidersSettingsTab'),
  'ProvidersSettingsTab',
);
const EnvVarsSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/EnvVarsSettingsTab/EnvVarsSettingsTab'),
  'EnvVarsSettingsTab',
);
const InstructionsSettingsTab = lazyNamed(
  () => import('@/components/settings/tabs/InstructionsSettingsTab/InstructionsSettingsTab'),
  'InstructionsSettingsTab',
);

const getErrorMessage = (error: unknown): string | undefined =>
  error instanceof Error ? error.message : undefined;

const tabLoadingFallback = (
  <div className={styles['tab-loading']}>
    <Spinner size="md" className={styles.spinner} />
  </div>
);

// The main pane of /settings/:section. The section list lives in the app
// sidebar (SettingsNav); this renders the selected section.
export function SettingsPage() {
  const section = findSettingsSection(useParams().section);
  useCommandMenu();
  const [isDeleteAllDialogOpen, setIsDeleteAllDialogOpen] = useState(false);

  const generalSecretFields = getGeneralSecretFields();

  const { data: settings, error: fetchError } = useSettingsQuery();
  const deleteAllChats = useDeleteAllChatsMutation();

  const [localSettings, setLocalSettings] = useState<UserSettings | null>(settings ?? null);
  const localSettingsRef = useRef<UserSettings | null>(localSettings);
  localSettingsRef.current = localSettings;

  const instantUpdateMutation = useUpdateSettingsMutation();

  const buildChangedPayload = useCallback(
    (current: UserSettings, previous: UserSettings): UserSettingsUpdate => {
      const payload: UserSettingsUpdate = {};
      const fields: (keyof UserSettingsUpdate)[] = [
        'github_personal_access_token',
        'custom_instructions',
        'custom_env_vars',
        'personas',
        'stream_actions',
        'notifications_enabled',
        'title_model_id',
        'auto_compact_window',
        'browser_tools_enabled',
      ];

      for (const field of fields) {
        if (JSON.stringify(current[field]) !== JSON.stringify(previous[field])) {
          // Union-key index → value type collapses to undefined; cast past it.
          payload[field] = (current[field] ?? null) as never;
        }
      }
      return payload;
    },
    [],
  );

  const persistSettings = useCallback(
    async (
      updater: (previous: UserSettings) => UserSettings,
      options: { errorMessage?: string } = {},
    ) => {
      if (!localSettingsRef.current) {
        throw new Error('Settings data is required before persisting changes');
      }

      const previousSettings = localSettingsRef.current;
      const updatedSettings = updater(previousSettings);

      setLocalSettings(updatedSettings);
      localSettingsRef.current = updatedSettings;

      try {
        const payload = buildChangedPayload(updatedSettings, previousSettings);
        if (Object.keys(payload).length === 0) return;

        const result = await instantUpdateMutation.mutateAsync(payload);
        setLocalSettings(result);
        localSettingsRef.current = result;
        // Stable id collapses rapid successive autosaves into one updating toast
        toast.success('Saved', { id: 'settings-saved' });
      } catch (error) {
        setLocalSettings(previousSettings);
        localSettingsRef.current = previousSettings;
        toast.error(options.errorMessage || getErrorMessage(error) || 'Failed to update settings');
        throw error;
      }
    },
    [instantUpdateMutation, buildChangedPayload],
  );

  const [revealedFields, setRevealedFields] = useState<Record<ApiFieldKey, boolean>>({
    github_personal_access_token: false,
  });

  // persistSettings already surfaces failures via toast, so swallow the rejection here.
  const handlePersistSecret = (field: ApiFieldKey, value: string) => {
    void persistSettings((prev) => ({ ...prev, [field]: value || null })).catch(() => undefined);
  };

  const handlePersistInstructions = (value: string) => {
    void persistSettings((prev) => ({ ...prev, custom_instructions: value || null })).catch(
      () => undefined,
    );
  };

  const toggleFieldVisibility = (field: ApiFieldKey) => {
    setRevealedFields((prev) => ({ ...prev, [field]: !prev[field] }));
  };

  const handleDeleteAllChats = () => {
    setIsDeleteAllDialogOpen(true);
  };

  const handleNotificationsEnabledChange = (enabled: boolean) => {
    void persistSettings((prev) => ({ ...prev, notifications_enabled: enabled })).catch(
      () => undefined,
    );
  };

  const handleTitleModelChange = (modelId: string) => {
    void persistSettings((prev) => ({ ...prev, title_model_id: modelId })).catch(() => undefined);
  };

  const handleAutoCompactWindowChange = (tokens: number) => {
    void persistSettings((prev) => ({ ...prev, auto_compact_window: tokens })).catch(
      () => undefined,
    );
  };

  const handleBrowserToolsEnabledChange = (enabled: boolean) => {
    void persistSettings((prev) => ({ ...prev, browser_tools_enabled: enabled })).catch(
      () => undefined,
    );
  };

  const confirmDeleteAllChats = async () => {
    try {
      await deleteAllChats.mutateAsync();
      toast.success('All chats deleted successfully');
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to delete all chats');
    } finally {
      setIsDeleteAllDialogOpen(false);
    }
  };

  useEffect(() => {
    if (settings) {
      setLocalSettings({ ...settings });
    }
  }, [settings]);

  const errorMessage = getErrorMessage(fetchError) ?? null;

  if (!section) return <Navigate to={settingsPath()} replace />;

  if (fetchError && !settings) {
    return (
      <div className={styles['status-screen']}>
        <div className={styles['status-text']}>Failed to load settings</div>
      </div>
    );
  }

  // First load, or the frame before the sync effect copies settings → localSettings.
  if (!settings || !localSettings) {
    return (
      <div className={styles['status-screen']}>
        <Spinner size="lg" className={styles.spinner} />
      </div>
    );
  }

  const panels: Record<SettingsSectionId, () => ReactNode> = {
    general: () => (
      <GeneralSettingsTab
        fields={generalSecretFields}
        settings={localSettings}
        revealedFields={revealedFields}
        onPersistSecret={handlePersistSecret}
        onToggleVisibility={toggleFieldVisibility}
        onDeleteAllChats={handleDeleteAllChats}
        onNotificationsEnabledChange={handleNotificationsEnabledChange}
        onTitleModelChange={handleTitleModelChange}
        onAutoCompactWindowChange={handleAutoCompactWindowChange}
        onBrowserToolsEnabledChange={handleBrowserToolsEnabledChange}
      />
    ),
    providers: () => <ProvidersSettingsTab />,
    projects: () => <WorkspacesSettingsTab />,
    skills: () => <SkillsSettingsTab />,
    personas: () => <PersonasSettingsTab />,
    'stream-actions': () => <StreamActionsSettingsTab />,
    automations: () => <AutomationsSettingsTab />,
    artifacts: () => <ArtifactsSettingsTab />,
    'env-vars': () => <EnvVarsSettingsTab />,
    instructions: () => (
      <InstructionsSettingsTab
        instructions={localSettings.custom_instructions || ''}
        onPersist={handlePersistInstructions}
      />
    ),
  };

  return (
    <div className={styles.content}>
      <div className={styles['content-inner']}>
        {errorMessage && (
          <div className={styles['error-box']}>
            <p className={styles['error-box-text']}>{errorMessage}</p>
          </div>
        )}

        <SettingsProvider
          localSettings={localSettings}
          setLocalSettings={setLocalSettings as React.Dispatch<React.SetStateAction<UserSettings>>}
          persistSettings={persistSettings}
          settings={settings}
        >
          {/* Keyed so a crashed section recovers when you switch to another. */}
          <ErrorBoundary key={section.id}>
            <Suspense fallback={tabLoadingFallback}>{panels[section.id]()}</Suspense>
          </ErrorBoundary>
        </SettingsProvider>
      </div>

      <ConfirmDialog
        isOpen={isDeleteAllDialogOpen}
        onClose={() => setIsDeleteAllDialogOpen(false)}
        onConfirm={confirmDeleteAllChats}
        title="Delete All Chats"
        message="Are you sure you want to delete all chats? This action cannot be undone."
        confirmLabel="Delete All"
        cancelLabel="Cancel"
      />

      {/* Search and the command palette work here too; no chat is in scope. */}
      <ChatProvider>
        <CommandMenu />
      </ChatProvider>
    </div>
  );
}
