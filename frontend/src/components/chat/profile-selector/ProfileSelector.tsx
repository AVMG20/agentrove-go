import { memo, useMemo } from 'react';
import { Link as RouterLink } from 'react-router-dom';
import clsx from 'clsx';
import { Settings2 } from 'lucide-react';
import { Dropdown, type DropdownPosition } from '@/components/ui/primitives/Dropdown/Dropdown';
import type { DropdownItemType } from '@/components/ui/primitives/Dropdown/Dropdown';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { providerIconComponent } from '@/components/ui/icons/providerIcons';
import { settingsPath } from '@/components/settings/settingsSections';
import { useAuthStore } from '@/store/authStore';
import { useProfileMap } from '@/hooks/queries/useProfileQueries';
import { useIsSplitMode } from '@/hooks/useIsSplitMode';
import { PROVIDER_NAMES, groupProfiles, profileStatusLabel } from '@/utils/profiles';
import type { Profile, ProviderKind } from '@/types/profile.types';
import styles from './ProfileSelector.module.scss';

interface ProfileOption {
  id: string;
  // `name` is what the dropdown's search matches.
  name: string;
  provider: ProviderKind;
  profile: Profile | null;
}

export interface ProfileSelectorProps {
  selectedProfileId: string;
  onProfileChange: (profileId: string) => void;
  // The thread has messages: picking another profile starts a new thread.
  locked?: boolean;
  // Shown when the selected profile is no longer in the list (deleted).
  fallbackProvider?: ProviderKind;
  dropdownPosition?: DropdownPosition;
  dropdownAlign?: 'left' | 'right';
  disabled?: boolean;
  variant?: 'default' | 'text';
}

function toOption(profile: Profile): ProfileOption {
  return { id: profile.id, name: profile.name, provider: profile.provider, profile };
}

// The composer's account picker: which provider profile the thread runs on.
export const ProfileSelector = memo(function ProfileSelector({
  selectedProfileId,
  onProfileChange,
  locked = false,
  fallbackProvider = 'claude',
  dropdownPosition = 'top',
  dropdownAlign,
  disabled = false,
  variant = 'default',
}: ProfileSelectorProps) {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const isSplitMode = useIsSplitMode();
  const { profiles, byId, isLoading } = useProfileMap(isAuthenticated);

  const items = useMemo(() => {
    const out: DropdownItemType<ProfileOption>[] = [];
    for (const group of groupProfiles(profiles)) {
      if (group.profiles.length === 0) continue;
      out.push({ type: 'header', label: PROVIDER_NAMES[group.provider] });
      group.profiles.forEach((p) => out.push({ type: 'item', data: toOption(p) }));
    }
    return out;
  }, [profiles]);

  if (isLoading) {
    return (
      <div className={styles.status} aria-label="Loading accounts">
        <div className={styles['status-pill']} />
      </div>
    );
  }
  // Nothing to choose between (or the list failed): the thread runs on its
  // profile regardless, so there's nothing to show.
  if (profiles.length === 0) return null;

  const selected = byId.get(selectedProfileId);
  const value: ProfileOption = selected
    ? toOption(selected)
    : { id: selectedProfileId, name: 'Deleted account', provider: fallbackProvider, profile: null };

  return (
    <Dropdown
      value={value}
      items={items}
      getItemKey={(option) => option.id}
      getItemLabel={(option) => option.name}
      onSelect={(option) => onProfileChange(option.id)}
      leftIcon={providerIconComponent(value.provider)}
      alwaysShowIcon
      width="17rem"
      dropdownPosition={dropdownPosition}
      dropdownAlign={dropdownAlign}
      disabled={disabled}
      compactOnMobile
      forceCompact={isSplitMode}
      searchable={profiles.length > 8}
      searchPlaceholder="Filter accounts..."
      searchVariant="underline"
      selectionStyle="accent"
      triggerVariant={variant}
      triggerClassName={clsx(!selected && styles['trigger--missing'])}
      renderHeader={
        locked
          ? () => (
              <p className={styles.note}>
                This thread&apos;s account is fixed. Picking another one starts a new thread.
              </p>
            )
          : undefined
      }
      renderFooter={() => (
        <RouterLink to={settingsPath('providers')} className={styles.manage}>
          <Settings2 aria-hidden className={styles['manage-icon']} />
          Manage accounts
        </RouterLink>
      )}
      renderItem={(option, isSelected) => {
        const profile = option.profile;
        const state = profile?.status ?? 'ready';
        return (
          <div className={styles.item}>
            <ProviderIcon provider={option.provider} className={styles['item-icon']} />
            <span className={styles['item-text']}>
              <span
                className={clsx(styles['item-name'], isSelected && styles['item-name--selected'])}
              >
                {option.name}
                {profile?.is_default && <span className={styles['item-default']}>Default</span>}
              </span>
              {profile && (
                <span className={clsx(styles['item-status'], styles[`item-status--${state}`])}>
                  {profileStatusLabel(profile)}
                </span>
              )}
            </span>
          </div>
        );
      }}
    />
  );
});
