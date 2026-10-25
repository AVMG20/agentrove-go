import { useId, useState, type FormEvent, type KeyboardEvent } from 'react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import { Label } from '@/components/ui/primitives/Label/Label';
import { FieldMessage } from '@/components/ui/primitives/FieldMessage/FieldMessage';
import { SegmentedControl } from '@/components/ui/primitives/SegmentedControl/SegmentedControl';
import { SecretInput } from '@/components/settings/inputs/SecretInput/SecretInput';
import { AUTH_MODES, authModeLabel } from '@/utils/profiles';
import type { Profile, ProfileAuthMode, ProfileInput, ProviderKind } from '@/types/profile.types';
import styles from './ProvidersSettingsTab.module.scss';

interface ProfileFormProps {
  provider: ProviderKind;
  // null adds a new account.
  profile: Profile | null;
  isSaving: boolean;
  error: string | null;
  onCancel: () => void;
  onSubmit: (input: ProfileInput) => void;
}

function tokenLabel(provider: ProviderKind, mode: ProfileAuthMode): string {
  if (mode === 'endpoint') return 'Auth token';
  return provider === 'claude' ? 'Claude token' : 'API key';
}

function tokenHint(provider: ProviderKind, mode: ProfileAuthMode, editing: boolean): string {
  if (editing) return 'Leave blank to keep the current one.';
  if (mode === 'endpoint') return 'Sent as ANTHROPIC_AUTH_TOKEN to the endpoint.';
  if (provider === 'claude') {
    return 'From claude setup-token. Leave blank to sign in from here after saving.';
  }
  return provider === 'codex' ? 'An OpenAI API key.' : 'A Gemini API key.';
}

// Inline add/edit form for one account.
export function ProfileForm({
  provider,
  profile,
  isSaving,
  error,
  onCancel,
  onSubmit,
}: ProfileFormProps) {
  const id = useId();
  const editing = profile !== null;
  const modes = AUTH_MODES[provider];
  const [name, setName] = useState(profile?.name ?? '');
  const [authMode, setAuthMode] = useState<ProfileAuthMode>(
    profile && modes.includes(profile.auth_mode) ? profile.auth_mode : modes[0],
  );
  const [baseUrl, setBaseUrl] = useState(profile?.base_url ?? '');
  const [usageUrl, setUsageUrl] = useState(profile?.usage_url ?? '');
  const [token, setToken] = useState('');
  const [tokenVisible, setTokenVisible] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const needsToken = authMode !== 'login';
  // A token is required where nothing else can sign the account in.
  const tokenRequired =
    needsToken &&
    !(editing && profile.has_token && profile.auth_mode !== 'login') &&
    (authMode === 'endpoint' || provider !== 'claude');

  const handleSubmit = (event: FormEvent) => {
    event.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setLocalError('Give the account a name.');
      return;
    }
    if (authMode === 'endpoint' && !baseUrl.trim()) {
      setLocalError('Enter the endpoint’s base URL.');
      return;
    }
    if (tokenRequired && !token.trim()) {
      setLocalError(`Enter the ${tokenLabel(provider, authMode).toLowerCase()}.`);
      return;
    }
    setLocalError(null);
    const input: ProfileInput = {
      name: trimmedName,
      auth_mode: authMode,
      base_url: authMode === 'endpoint' ? baseUrl.trim() : '',
      usage_url: authMode === 'endpoint' ? usageUrl.trim() : '',
    };
    if (!editing) input.provider = provider;
    if (needsToken && token.trim()) input.token = token.trim();
    onSubmit(input);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLFormElement>) => {
    if (event.key === 'Escape' && !event.defaultPrevented) {
      event.preventDefault();
      onCancel();
    }
  };

  const shownError = localError ?? error;

  return (
    <form
      className={styles.form}
      onSubmit={handleSubmit}
      onKeyDown={handleKeyDown}
      aria-label={editing ? `Edit ${profile.name}` : 'Add account'}
      noValidate
    >
      <div className={styles.field}>
        <Label htmlFor={`${id}-name`}>Name</Label>
        <Input
          id={`${id}-name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Work, Personal"
          maxLength={60}
          autoFocus
          autoComplete="off"
        />
      </div>

      {modes.length > 1 && (
        <div className={styles.field}>
          <span className={styles['field-label']} id={`${id}-mode`}>
            Sign in with
          </span>
          <div role="group" aria-labelledby={`${id}-mode`}>
            <SegmentedControl<ProfileAuthMode>
              size="sm"
              value={authMode}
              onChange={(mode) => {
                setAuthMode(mode);
                setLocalError(null);
              }}
              options={modes.map((mode) => ({
                value: mode,
                label: authModeLabel(provider, mode),
              }))}
            />
          </div>
          {authMode === 'login' && (
            <FieldMessage>
              {provider === 'codex'
                ? 'After saving, sign in with your ChatGPT account using a device code.'
                : 'After saving, sign in with your Google account.'}
            </FieldMessage>
          )}
        </div>
      )}

      {authMode === 'endpoint' && (
        <>
          <div className={styles.field}>
            <Label htmlFor={`${id}-base`}>Base URL</Label>
            <Input
              id={`${id}-base`}
              type="url"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="https://gateway.example.com"
              autoComplete="off"
            />
          </div>
          <div className={styles.field}>
            <Label htmlFor={`${id}-usage`}>Usage URL (optional)</Label>
            <Input
              id={`${id}-usage`}
              type="url"
              value={usageUrl}
              onChange={(e) => setUsageUrl(e.target.value)}
              placeholder="https://gateway.example.com/usage"
              autoComplete="off"
            />
            <FieldMessage>Where the composer’s plan-usage meter reads from.</FieldMessage>
          </div>
        </>
      )}

      {needsToken && (
        <div className={styles.field}>
          <Label htmlFor={`${id}-token`}>
            {tokenLabel(provider, authMode)}
            {editing && profile.has_token && profile.token_hint && (
              <span className={styles['token-hint']}> (current {profile.token_hint})</span>
            )}
          </Label>
          <SecretInput
            id={`${id}-token`}
            value={token}
            placeholder={editing && profile.has_token ? 'Blank keeps current' : 'Paste here'}
            isVisible={tokenVisible}
            onChange={setToken}
            onToggleVisibility={() => setTokenVisible((v) => !v)}
          />
          <FieldMessage>{tokenHint(provider, authMode, editing && profile.has_token)}</FieldMessage>
        </div>
      )}

      {shownError && (
        <FieldMessage variant="error" className={styles['form-error']}>
          <span role="alert">{shownError}</span>
        </FieldMessage>
      )}

      <div className={styles['form-actions']}>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={isSaving}>
          Cancel
        </Button>
        <Button type="submit" size="sm" isLoading={isSaving} disabled={isSaving}>
          {editing ? 'Save' : 'Add account'}
        </Button>
      </div>
    </form>
  );
}
