import { ProfileSelector } from '@/components/chat/profile-selector/ProfileSelector';
import { useInputActions, useInputState } from '@/hooks/useInputContext';
import { PlanUsageIndicator } from './PlanUsageIndicator';
import styles from './ComposerAccount.module.scss';

// The footer's left side: the account the thread runs on, then that account's
// plan usage, so it's clear whose limits the bar shows.
export function ComposerAccount() {
  const state = useInputState();
  const actions = useInputActions();
  const hasPicker = !!actions.onProfileChange && !!state.selectedProfileId;
  // Plan usage is Claude's (subscription windows).
  const showUsage = state.agentKind === 'claude';
  if (!hasPicker && !showUsage) return null;

  return (
    <div className={styles.account}>
      {hasPicker && (
        <ProfileSelector
          selectedProfileId={state.selectedProfileId!}
          onProfileChange={actions.onProfileChange!}
          locked={state.profileLocked}
          fallbackProvider={state.agentKind}
          dropdownPosition={state.dropdownPosition}
          dropdownAlign="left"
          disabled={state.isLoading && !state.profileLocked}
          variant="text"
        />
      )}
      {showUsage && (
        <PlanUsageIndicator profileId={state.selectedProfileId} showSpark={!hasPicker} />
      )}
    </div>
  );
}
