import { useState } from 'react';
import toast from 'react-hot-toast';
import { Edit2, Loader2, LogIn, Plus, Trash2 } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import {
  useCreateProfileMutation,
  useDeleteProfileMutation,
  useProfilesQuery,
  useSetDefaultProfileMutation,
  useStartProfileLoginMutation,
  useUpdateProfileMutation,
} from '@/hooks/queries/useProfileQueries';
import {
  PROVIDER_CLI,
  PROVIDER_NAMES,
  canSignIn,
  offersSignIn,
  groupProfiles,
  profileChip,
  profileStatusLabel,
  removeProfileMessage,
} from '@/utils/profiles';
import type { Profile, ProfileInput, ProviderKind } from '@/types/profile.types';
import { ProfileForm } from './ProfileForm';
import { ProfileSignIn } from './ProfileSignIn';
import styles from './ProvidersSettingsTab.module.scss';

// Which form is open: a new account for a provider, or an existing one.
type Editing = { provider: ProviderKind; profileId: string | null } | null;

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error && error.message ? error.message : fallback;

export function ProvidersSettingsTab() {
  const { data: profiles, isLoading, error, refetch } = useProfilesQuery();
  const createProfile = useCreateProfileMutation();
  const updateProfile = useUpdateProfileMutation();
  const deleteProfile = useDeleteProfileMutation();
  const setDefault = useSetDefaultProfileMutation();
  const startLogin = useStartProfileLoginMutation();

  const [editing, setEditing] = useState<Editing>(null);
  const [formError, setFormError] = useState<string | null>(null);
  // Accounts whose sign-in panel is open.
  const [signingIn, setSigningIn] = useState<Set<string>>(() => new Set());
  // Accounts whose sign-in is still being started (POST /login pending).
  const [starting, setStarting] = useState<Set<string>>(() => new Set());
  const [pendingDelete, setPendingDelete] = useState<Profile | null>(null);

  const openForm = (next: Editing) => {
    setFormError(null);
    setEditing(next);
  };

  const toggle = (set: typeof setSigningIn) => (profileId: string, on: boolean) =>
    set((prev) => {
      const next = new Set(prev);
      if (on) next.add(profileId);
      else next.delete(profileId);
      return next;
    });
  const showSignIn = toggle(setSigningIn);
  const markStarting = toggle(setStarting);

  const beginSignIn = (profile: Profile) => {
    showSignIn(profile.id, true);
    markStarting(profile.id, true);
    startLogin.mutate(profile.id, {
      onError: (err) => {
        showSignIn(profile.id, false);
        toast.error(errorText(err, 'Failed to start sign-in'));
      },
      onSettled: () => markStarting(profile.id, false),
    });
  };

  const handleSubmit = async (input: ProfileInput) => {
    if (!editing) return;
    setFormError(null);
    try {
      if (editing.profileId) {
        await updateProfile.mutateAsync({ profileId: editing.profileId, data: input });
        setEditing(null);
        return;
      }
      const created = await createProfile.mutateAsync(input);
      setEditing(null);
      // Nothing signs a fresh login-mode account in but the CLI: start it now.
      if (canSignIn(created) && created.status === 'needs_sign_in') beginSignIn(created);
    } catch (err) {
      setFormError(errorText(err, 'Failed to save the account'));
    }
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    const target = pendingDelete;
    setPendingDelete(null);
    try {
      await deleteProfile.mutateAsync(target.id);
      if (editing?.profileId === target.id) setEditing(null);
      toast.success(`Removed ${target.name}`);
    } catch (err) {
      toast.error(errorText(err, 'Failed to remove the account'));
    }
  };

  const handleSetDefault = (profile: Profile) => {
    setDefault.mutate(profile.id, {
      onError: (err) => toast.error(errorText(err, 'Failed to set the default account')),
    });
  };

  const isSaving = createProfile.isPending || updateProfile.isPending;

  const renderForm = (provider: ProviderKind, profile: Profile | null) => (
    <ProfileForm
      key={profile?.id ?? `new-${provider}`}
      provider={provider}
      profile={profile}
      isSaving={isSaving}
      error={formError}
      onCancel={() => setEditing(null)}
      onSubmit={handleSubmit}
    />
  );

  const renderRow = (profile: Profile) => {
    if (editing?.profileId === profile.id) {
      return (
        <li key={profile.id} className={styles.card}>
          {renderForm(profile.provider, profile)}
        </li>
      );
    }
    const signInOpen = signingIn.has(profile.id) || profile.login?.status === 'running';
    const deleting = deleteProfile.isPending && deleteProfile.variables === profile.id;
    const settingDefault = setDefault.isPending && setDefault.variables === profile.id;
    return (
      <li key={profile.id} className={styles.card}>
        <div className={styles.row}>
          <div className={styles['row-main']}>
            <div className={styles['row-title']}>
              <span className={styles['row-name']}>{profile.name}</span>
              <span className={styles.chip}>{profileChip(profile)}</span>
              {profile.is_default && <span className={styles['default-badge']}>Default</span>}
            </div>
            <p className={clsx(styles.status, styles[`status--${profile.status}`])}>
              <span aria-hidden className={styles['status-dot']} />
              <span className={styles['status-state']}>
                {profile.status === 'ready' ? 'Ready' : profileStatusLabel(profile)}
              </span>
              {profile.status === 'ready' && profileStatusLabel(profile) !== 'Ready' && (
                <span className={styles['status-account']}>{profileStatusLabel(profile)}</span>
              )}
              {profile.status === 'cli_missing' && (
                <span className={styles['status-account']}>
                  {profile.status_detail ||
                    `Install ${PROVIDER_CLI[profile.provider]} in the box to use this account.`}
                </span>
              )}
              {profile.auth_mode === 'endpoint' && profile.base_url && (
                <span className={styles['status-account']}>{profile.base_url}</span>
              )}
            </p>
          </div>
          <div className={styles['row-actions']}>
            {!profile.is_default && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className={styles['text-btn']}
                onClick={() => handleSetDefault(profile)}
                disabled={settingDefault}
              >
                Set default
              </Button>
            )}
            {offersSignIn(profile) && !signInOpen && (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className={styles['action-btn']}
                onClick={() => beginSignIn(profile)}
                aria-label={`Sign in ${profile.name}`}
                title={profile.status === 'ready' ? 'Sign in again' : 'Sign in'}
              >
                <LogIn className={styles['action-icon']} />
              </Button>
            )}
            {!profile.builtin && (
              <>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={styles['action-btn']}
                  onClick={() => openForm({ provider: profile.provider, profileId: profile.id })}
                  aria-label={`Edit ${profile.name}`}
                  title="Edit"
                >
                  <Edit2 className={styles['action-icon']} />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={styles['action-btn']}
                  onClick={() => setPendingDelete(profile)}
                  aria-label={`Remove ${profile.name}`}
                  title="Remove"
                  disabled={deleting}
                >
                  {deleting ? (
                    <Loader2 className={clsx(styles['action-icon'], styles.spin)} />
                  ) : (
                    <Trash2 className={styles['action-icon']} />
                  )}
                </Button>
              </>
            )}
          </div>
        </div>
        {signInOpen && (
          <ProfileSignIn
            profile={profile}
            starting={starting.has(profile.id)}
            onClose={() => showSignIn(profile.id, false)}
            onRetry={() => beginSignIn(profile)}
          />
        )}
      </li>
    );
  };

  return (
    <div className={styles.tab}>
      <div className={styles.header}>
        <h2 className={styles.title}>Providers</h2>
        <p className={styles.description}>
          The agent accounts your threads run on. Each account keeps its own sign-in and config in
          your box. Pick one per thread in the composer; new threads start on the default.
        </p>
      </div>

      {isLoading ? (
        <div className={styles.loading} role="status" aria-label="Loading accounts">
          <Loader2 className={clsx(styles['loading-icon'], styles.spin)} />
        </div>
      ) : error || !profiles ? (
        <div className={styles.empty} role="alert">
          <p className={styles['empty-text']}>{errorText(error, 'Failed to load accounts')}</p>
          <Button type="button" variant="outline" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        </div>
      ) : (
        groupProfiles(profiles).map((group) => {
          const count = group.profiles.length;
          const adding = editing?.provider === group.provider && editing.profileId === null;
          const headingId = `providers-${group.provider}`;
          return (
            <section key={group.provider} className={styles.group} aria-labelledby={headingId}>
              <div className={styles['group-header']}>
                <h3 id={headingId} className={styles['group-title']}>
                  <ProviderIcon provider={group.provider} className={styles['group-icon']} />
                  {PROVIDER_NAMES[group.provider]}
                  <span className={styles['group-count']}>
                    · {count} {count === 1 ? 'account' : 'accounts'}
                  </span>
                </h3>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => openForm({ provider: group.provider, profileId: null })}
                  disabled={adding}
                >
                  <Plus className={styles['add-icon']} />
                  Add account
                </Button>
              </div>
              {count === 0 && !adding ? (
                <p className={styles['group-empty']}>
                  No {PROVIDER_NAMES[group.provider]} accounts yet.
                </p>
              ) : (
                <ul className={styles.list}>
                  {group.profiles.map(renderRow)}
                  {adding && (
                    <li className={clsx(styles.card, styles['card--new'])}>
                      {renderForm(group.provider, null)}
                    </li>
                  )}
                </ul>
              )}
            </section>
          );
        })
      )}

      <ConfirmDialog
        isOpen={pendingDelete !== null}
        onClose={() => setPendingDelete(null)}
        onConfirm={() => void handleDelete()}
        title="Remove account"
        message={pendingDelete ? removeProfileMessage(pendingDelete) : ''}
        confirmLabel="Remove"
      />
    </div>
  );
}
