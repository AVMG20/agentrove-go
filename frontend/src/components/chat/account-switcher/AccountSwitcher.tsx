import { useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Link as RouterLink } from 'react-router-dom';
import clsx from 'clsx';
import { Check, ChevronLeft, ChevronRight, Plus, Settings2 } from 'lucide-react';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { RefreshButton } from '@/components/ui/shared/RefreshButton/RefreshButton';
import { settingsPath } from '@/components/settings/settingsSections';
import { useAuthStore } from '@/store/authStore';
import { useProfileMap } from '@/hooks/queries/useProfileQueries';
import { useAccountUsage } from '@/hooks/queries/useUsageQueries';
import { useDropdown } from '@/hooks/useDropdown';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useIsSplitMode } from '@/hooks/useIsSplitMode';
import { useNow } from '@/hooks/useNow';
import {
  PROVIDER_NAMES,
  accountLine,
  groupProfiles,
  noUsageReason,
  profileStatusLabel,
} from '@/utils/profiles';
import { formatResetInShort, summarizeWindow, windowLabels } from '@/utils/planUsage';
import type { Profile, ProviderKind } from '@/types/profile.types';
import type { AccountUsage, UsageWindow } from '@/types/usage.types';
import { UsageBar } from './UsageBar';
import styles from './AccountSwitcher.module.scss';

export interface AccountSwitcherProps {
  selectedProfileId: string;
  onProfileChange: (profileId: string) => void;
  // The thread has messages: picking another account starts a new thread.
  locked?: boolean;
  // Shown when the selected account is gone (deleted, or none on old threads).
  fallbackProvider?: ProviderKind;
  disabled?: boolean;
}

type View = 'usage' | 'accounts';

const providersSettings = (provider?: ProviderKind) =>
  provider ? `${settingsPath('providers')}?add=${provider}` : settingsPath('providers');

// "2% 2h 9m": a window's use and time to reset, for the trigger.
function windowText(window: UsageWindow, now: number): string {
  const { used, resetLabel } = summarizeWindow(window, now);
  return [`${Math.round(used)}%`, resetLabel].filter(Boolean).join(' ');
}

function updatedLabel(fetchedAt: string, now: number): string {
  const mins = Math.floor((now - Date.parse(fetchedAt)) / 60_000);
  if (!Number.isFinite(mins) || mins < 1) return 'Updated just now';
  if (mins < 60) return `Updated ${mins}m ago`;
  return `Updated ${Math.floor(mins / 60)}h ago`;
}

const hasWindows = (usage: AccountUsage | undefined): usage is AccountUsage =>
  usage?.status === 'ok' && usage.windows.length > 0;

// The composer footer's account control: the thread's account with its plan
// usage, opening a card with the usage per window and the account list (each
// account with its own usage) to switch from.
export function AccountSwitcher({
  selectedProfileId,
  onProfileChange,
  locked = false,
  fallbackProvider = 'claude',
  disabled = false,
}: AccountSwitcherProps) {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const { profiles, byId, isLoading } = useProfileMap(isAuthenticated);
  const selected = byId.get(selectedProfileId) ?? null;
  const { usage, isRefreshing, refresh } = useAccountUsage(selectedProfileId, !!selected);
  const now = useNow(30_000);

  const sheetRef = useRef<HTMLDivElement>(null);
  const { isOpen, setIsOpen, dropdownRef } = useDropdown(sheetRef);
  const isMobile = useIsMobile();
  // Split panes are narrow: the bar stays, the numbers move into the popover.
  const isSplitMode = useIsSplitMode();
  const [view, setView] = useState<View>('usage');
  const panelId = useId();

  useEffect(() => {
    if (!isOpen) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return;
      event.preventDefault();
      setIsOpen(false);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, [isOpen, setIsOpen]);

  if (isLoading) {
    return (
      <div className={styles.loading} aria-label="Loading accounts">
        <div className={styles['loading-pill']} />
      </div>
    );
  }

  if (profiles.length === 0) {
    return (
      <RouterLink
        to={providersSettings()}
        className={clsx(styles.trigger, styles['trigger--empty'])}
      >
        <Plus aria-hidden className={styles['trigger-icon']} />
        <span className={styles['trigger-name']}>Add an account</span>
      </RouterLink>
    );
  }

  const provider = selected?.provider ?? fallbackProvider;
  const showUsage = !!selected && hasWindows(usage);
  const first = showUsage ? summarizeWindow(usage.windows[0], now) : null;
  const usageText = showUsage ? usage.windows.map((w) => windowText(w, now)).join(' · ') : '';
  // Phones have room for the first window only, its reset to the hour: "5% 4h".
  const compactUsage = first
    ? [`${Math.round(first.used)}%`, first.resetIn != null && formatResetInShort(first.resetIn)]
        .filter(Boolean)
        .join(' ')
    : '';

  const open = () => {
    // Without an account there's no usage to show: go straight to the list.
    setView(selected ? 'usage' : 'accounts');
    setIsOpen(true);
  };

  const pick = (profile: Profile) => {
    setIsOpen(false);
    onProfileChange(profile.id);
  };

  const usageView = selected && (
    <>
      <div className={styles.header}>
        <span className={styles['header-tile']}>
          <ProviderIcon provider={provider} className={styles['header-icon']} />
        </span>
        <div className={styles['header-text']}>
          <span className={styles.title}>{PROVIDER_NAMES[provider]} usage</span>
          {usage?.status === 'ok' && (
            <span className={styles.subtitle}>{updatedLabel(usage.fetched_at, now)}</span>
          )}
        </div>
        {usage && usage.status !== 'unavailable' && (
          <RefreshButton
            onClick={() => void refresh()}
            isRefreshing={isRefreshing}
            title="Refresh usage"
            ariaLabel="Refresh usage"
          />
        )}
      </div>

      <button
        type="button"
        className={styles['account-row']}
        onClick={() => setView('accounts')}
        aria-label={`Switch account (current: ${selected.name})`}
      >
        <span className={styles['account-name']}>{selected.name}</span>
        <span
          className={clsx(
            styles['account-meta'],
            selected.status !== 'ready' && styles['account-meta--warning'],
          )}
        >
          {selected.status === 'ready' ? accountLine(selected) : profileStatusLabel(selected)}
        </span>
        <ChevronRight aria-hidden className={styles['account-chevron']} />
      </button>

      {showUsage ? (
        <div className={styles.windows}>
          {usage.windows.map((w) => {
            const { used, resetLabel, tone } = summarizeWindow(w, now);
            const { long } = windowLabels(w.window_seconds);
            return (
              <div key={w.window_seconds} className={styles.window}>
                <div className={styles['window-row']}>
                  <span className={styles['window-name']}>{long}</span>
                  <span className={styles['window-used']}>{Math.round(used)}% used</span>
                </div>
                <UsageBar used={used} tone={tone} label={`${long} usage`} />
                <div className={clsx(styles['window-row'], styles['window-row--meta'])}>
                  <span>{Math.round(100 - used)}% remaining</span>
                  {resetLabel && <span>Resets in {resetLabel}</span>}
                </div>
              </div>
            );
          })}
        </div>
      ) : !usage && selected.status === 'ready' ? (
        <div className={styles['window-loading']} role="status" aria-label="Loading usage" />
      ) : (
        <p className={styles.empty}>
          {usage?.status === 'error'
            ? (usage.error ?? 'Usage unavailable right now.')
            : noUsageReason(selected)}
          {selected.status !== 'ready' && (
            <>
              {' '}
              <RouterLink
                to={settingsPath('providers')}
                className={styles['empty-link']}
                onClick={() => setIsOpen(false)}
              >
                Open Providers
              </RouterLink>
            </>
          )}
        </p>
      )}
    </>
  );

  const accountsView = (
    <>
      <div className={styles.header}>
        {selected && (
          <button
            type="button"
            className={styles.back}
            onClick={() => setView('usage')}
            aria-label="Back to usage"
          >
            <ChevronLeft aria-hidden className={styles['back-icon']} />
          </button>
        )}
        <span className={styles.title}>Accounts</span>
      </div>
      <p className={styles.note}>
        {locked
          ? 'This thread stays on its account. Picking another one starts a new thread.'
          : 'Each thread stays on the account that started it.'}
      </p>
      <div className={styles.accounts}>
        {groupProfiles(profiles)
          .filter((group) => group.profiles.length > 0)
          .map((group, _, groups) => (
            <div
              key={group.provider}
              className={styles.group}
              role="group"
              aria-label={PROVIDER_NAMES[group.provider]}
            >
              {groups.length > 1 && (
                <div className={styles['group-label']}>
                  <ProviderIcon provider={group.provider} className={styles['group-icon']} />
                  {PROVIDER_NAMES[group.provider]}
                </div>
              )}
              {group.profiles.map((profile) => (
                <AccountRow
                  key={profile.id}
                  profile={profile}
                  selected={profile.id === selectedProfileId}
                  now={now}
                  onPick={() => pick(profile)}
                />
              ))}
            </div>
          ))}
      </div>
      <div className={styles.links}>
        <RouterLink
          to={providersSettings(provider)}
          className={styles.link}
          onClick={() => setIsOpen(false)}
        >
          <Plus aria-hidden className={styles['link-icon']} />
          Add account
        </RouterLink>
        <RouterLink
          to={providersSettings()}
          className={styles.link}
          onClick={() => setIsOpen(false)}
        >
          <Settings2 aria-hidden className={styles['link-icon']} />
          Manage accounts
        </RouterLink>
      </div>
    </>
  );

  const panel = (
    <div
      id={panelId}
      role="dialog"
      aria-label={view === 'usage' ? `${PROVIDER_NAMES[provider]} usage` : 'Accounts'}
      className={clsx(styles.panel, isMobile && styles['panel--sheet'])}
    >
      {view === 'usage' && usageView ? usageView : accountsView}
    </div>
  );

  return (
    <div ref={dropdownRef} className={styles.switcher}>
      <button
        type="button"
        className={clsx(
          styles.trigger,
          isOpen && styles['trigger--open'],
          !selected && styles['trigger--missing'],
        )}
        onClick={() => (isOpen ? setIsOpen(false) : open())}
        disabled={disabled}
        aria-expanded={isOpen}
        aria-controls={isOpen ? panelId : undefined}
        aria-haspopup="dialog"
        aria-label={`Account: ${selected?.name ?? 'none'}${usageText ? `, usage ${usageText}` : ''}`}
      >
        <ProviderIcon provider={provider} className={styles['trigger-icon']} />
        <span className={styles['trigger-name']}>{selected?.name ?? 'No account'}</span>
        {selected && selected.status !== 'ready' && (
          <span className={styles['trigger-warning']} title={profileStatusLabel(selected)} />
        )}
        {first && (
          <UsageBar used={first.used} tone={first.tone} className={styles['trigger-bar']} />
        )}
        {isMobile
          ? compactUsage && <span className={styles['trigger-usage-compact']}>{compactUsage}</span>
          : usageText &&
            !isSplitMode && <span className={styles['trigger-usage']}>{usageText}</span>}
      </button>

      {isOpen &&
        (isMobile
          ? createPortal(
              <div
                ref={sheetRef}
                className={styles['sheet-layer']}
                onMouseDown={(e) => e.stopPropagation()}
              >
                <div className={styles.scrim} onClick={() => setIsOpen(false)} aria-hidden />
                {panel}
              </div>,
              document.body,
            )
          : panel)}
    </div>
  );
}

// One account in the list: name, who it runs as, its state and its usage
// windows. Each row reads its own usage (shared with the trigger's query).
function AccountRow({
  profile,
  selected,
  now,
  onPick,
}: {
  profile: Profile;
  selected: boolean;
  now: number;
  onPick: () => void;
}) {
  const { usage } = useAccountUsage(profile.id);
  const line = accountLine(profile);
  const ready = profile.status === 'ready';
  return (
    <button
      type="button"
      className={clsx(styles.row, selected && styles['row--selected'])}
      onClick={onPick}
      aria-pressed={selected}
    >
      <span className={styles['row-head']}>
        <span className={styles['row-name']}>{profile.name}</span>
        {line && <span className={styles['row-meta']}>{line}</span>}
        {profile.is_default && <span className={styles['row-chip']}>Default</span>}
        {selected && <Check aria-hidden className={styles['row-check']} />}
      </span>
      <span className={styles['row-body']}>
        <span className={clsx(styles.state, styles[`state--${profile.status}`])}>
          <span aria-hidden className={styles['state-dot']} />
          {ready ? 'Ready' : profileStatusLabel(profile)}
        </span>
        {hasWindows(usage) &&
          usage.windows.map((w) => {
            const { used, resetLabel, tone } = summarizeWindow(w, now);
            const { short, long } = windowLabels(w.window_seconds);
            return (
              <span key={w.window_seconds} className={styles['row-window']} title={long}>
                <span className={styles['row-window-text']}>
                  <span>{[short, resetLabel].filter(Boolean).join(' · ')}</span>
                  <span className={styles['row-window-used']}>{Math.round(used)}%</span>
                </span>
                <UsageBar used={used} tone={tone} />
              </span>
            );
          })}
      </span>
    </button>
  );
}
