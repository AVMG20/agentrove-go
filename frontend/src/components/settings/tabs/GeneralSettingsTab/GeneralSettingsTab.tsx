import toast from 'react-hot-toast';
import { useDraftField } from '@/hooks/useDraftField';
import {
  useAuthConfigQuery,
  useCurrentUserQuery,
  useSetRegistrationMutation,
} from '@/hooks/queries/useAuthQueries';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Select } from '@/components/ui/primitives/Select/Select';
import { Switch } from '@/components/ui/primitives/Switch/Switch';
import type { ApiFieldKey, GeneralSecretFieldConfig } from '@/types/settings.types';
import type { UserSettings } from '@/types/user.types';
import type { Theme } from '@/types/ui.types';
import { useUIStore } from '@/store/uiStore';
import { SecretInput } from '@/components/settings/inputs/SecretInput/SecretInput';
import { ModelSelector } from '@/components/chat/model-selector/ModelSelector';
import { THEMES } from '@/utils/theme';
import styles from './GeneralSettingsTab.module.scss';

interface GeneralSettingsTabProps {
  fields: GeneralSecretFieldConfig[];
  settings: UserSettings;
  revealedFields: Record<ApiFieldKey, boolean>;
  onPersistSecret: (field: ApiFieldKey, value: string) => void;
  onToggleVisibility: (field: ApiFieldKey) => void;
  onDeleteAllChats: () => void;
  onNotificationsEnabledChange: (enabled: boolean) => void;
  onTitleModelChange: (modelId: string) => void;
  onAutoCompactWindowChange: (tokens: number) => void;
  onBrowserToolsEnabledChange: (enabled: boolean) => void;
}

function SectionCard({
  title,
  children,
  className,
}: {
  title: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={clsx(styles['section-card'], className)}>
      <h2 className={styles['section-title']}>{title}</h2>
      {children}
    </div>
  );
}

const AUTO_COMPACT_WINDOWS = [
  { value: 100_000, label: '100k' },
  { value: 250_000, label: '250k' },
  { value: 500_000, label: '500k' },
  { value: 750_000, label: '750k' },
  { value: 1_000_000, label: '1M' },
];

// Dropdown rather than a row of buttons — 9 themes is too many to lay out inline.
// Width is bounded on the wrapper since Select itself is w-full.
function ThemeControl() {
  const theme = useUIStore((state) => state.theme);
  return (
    <div className={styles['theme-control']}>
      <Select
        value={theme}
        onChange={(e) => useUIStore.getState().setTheme(e.target.value as Theme)}
        className={styles['theme-select']}
        aria-label="Theme"
      >
        {THEMES.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </Select>
    </div>
  );
}

function SecretField({
  field,
  savedValue,
  isVisible,
  onPersist,
  onToggleVisibility,
}: {
  field: GeneralSecretFieldConfig;
  savedValue: string;
  isVisible: boolean;
  onPersist: (field: ApiFieldKey, value: string) => void;
  onToggleVisibility: (field: ApiFieldKey) => void;
}) {
  const { draft, setDraft, handleBlur } = useDraftField(savedValue, (v) => onPersist(field.key, v));

  return (
    <div>
      <div className={styles['field-header']}>
        <div>
          <h3 className={styles['setting-title']}>{field.label}</h3>
          <p className={styles['field-desc']}>{field.description}</p>
        </div>
      </div>
      <SecretInput
        value={draft}
        placeholder={field.placeholder}
        isVisible={isVisible}
        onChange={setDraft}
        onBlur={handleBlur}
        onToggleVisibility={() => onToggleVisibility(field.key)}
        helperText={field.helperText}
      />
    </div>
  );
}

// Superuser-only: whether new accounts can sign up. When REGISTRATION_DISABLED
// pins it off on the server there's nothing to change here, so the card hides.
function AdministrationCard() {
  const { data: config } = useAuthConfigQuery();
  const setRegistration = useSetRegistrationMutation({
    onSuccess: (next) =>
      toast.success(next.registration_enabled ? 'Sign-ups enabled' : 'Sign-ups disabled', {
        id: 'settings-saved',
      }),
    onError: (error) => toast.error(error.message || 'Failed to update sign-ups'),
  });
  if (!config || config.registration_locked) return null;

  return (
    <SectionCard title="Administration">
      <div className={styles['pref-row-notif']}>
        <div className={styles['pref-text']}>
          <h3 className={styles['setting-title']}>Allow new sign-ups</h3>
          <p className={styles['field-desc']}>
            Let anyone with the link create an account on this instance.
          </p>
        </div>
        <Switch
          checked={config.registration_enabled}
          onCheckedChange={(enabled) => setRegistration.mutate(enabled)}
          disabled={setRegistration.isPending}
          aria-label="Allow new sign-ups"
        />
      </div>
    </SectionCard>
  );
}

export function GeneralSettingsTab({
  fields,
  settings,
  revealedFields,
  onPersistSecret,
  onToggleVisibility,
  onDeleteAllChats,
  onNotificationsEnabledChange,
  onTitleModelChange,
  onAutoCompactWindowChange,
  onBrowserToolsEnabledChange,
}: GeneralSettingsTabProps) {
  const isSuperuser = useCurrentUserQuery().data?.is_superuser ?? false;

  return (
    <div className={styles.general}>
      <SectionCard title="API Keys & Authentication">
        <div className={styles.stack}>
          {fields.map((field) => (
            <SecretField
              key={field.key}
              field={field}
              savedValue={settings[field.key] ?? ''}
              isVisible={revealedFields[field.key]}
              onPersist={onPersistSecret}
              onToggleVisibility={onToggleVisibility}
            />
          ))}
        </div>
      </SectionCard>

      <SectionCard title="Preferences">
        <div className={styles.divided}>
          <div className={styles['pref-row']}>
            <h3 className={styles['setting-title']}>Theme</h3>
            <ThemeControl />
          </div>
          <div className={styles['pref-row-notif']}>
            <div className={styles['pref-text']}>
              <h3 className={styles['setting-title']}>Notifications</h3>
              <p className={styles['field-desc']}>
                Send notifications for permission requests, questions, and finished or failed tasks.
              </p>
            </div>
            <Switch
              checked={settings.notifications_enabled}
              onCheckedChange={onNotificationsEnabledChange}
              aria-label="Notifications"
            />
          </div>
          <div className={styles['pref-row-notif']}>
            <div className={styles['pref-text']}>
              <h3 className={styles['setting-title']}>Title Generation Model</h3>
              <p className={styles['field-desc']}>Model used to generate chat titles.</p>
            </div>
            <ModelSelector
              selectedModelId={settings.title_model_id}
              onModelChange={onTitleModelChange}
              compact={false}
              dropdownAlign="right"
            />
          </div>
          <div className={styles['pref-row-notif']}>
            <div className={styles['pref-text']}>
              <h3 className={styles['setting-title']}>Auto-compact</h3>
              <p className={styles['field-desc']}>
                Claude summarizes the conversation once its context reaches this many tokens.
              </p>
            </div>
            <div className={styles['theme-control']}>
              <Select
                value={settings.auto_compact_window}
                onChange={(e) => onAutoCompactWindowChange(Number(e.target.value))}
                className={styles['theme-select']}
                aria-label="Auto-compact"
              >
                {AUTO_COMPACT_WINDOWS.map((opt) => (
                  <option key={opt.value} value={opt.value}>
                    {opt.label}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          {settings.browser_available !== false && (
            <div className={styles['pref-row-notif']}>
              <div className={styles['pref-text']}>
                <h3 className={styles['setting-title']}>Browser tools</h3>
                <p className={styles['field-desc']}>
                  Let agents drive the headless browser in your box to test web apps. It starts on
                  first use and stops when idle; you can watch it in the terminal panel&apos;s
                  Browser tab.
                </p>
              </div>
              <Switch
                checked={settings.browser_tools_enabled}
                onCheckedChange={onBrowserToolsEnabledChange}
                aria-label="Browser tools"
              />
            </div>
          )}
        </div>
      </SectionCard>

      {isSuperuser && <AdministrationCard />}

      <SectionCard title="Data Management">
        <div className={styles['data-row']}>
          <div className={styles['pref-text']}>
            <h3 className={styles['setting-title']}>Delete All Chats</h3>
            <p className={styles['field-desc']}>
              Permanently delete all chat history. This action cannot be undone.
            </p>
          </div>
          <Button
            type="button"
            onClick={onDeleteAllChats}
            variant="outline"
            size="sm"
            className={styles['delete-button']}
          >
            Delete All
          </Button>
        </div>
      </SectionCard>
    </div>
  );
}
