import { AccountSwitcher } from '@/components/chat/account-switcher/AccountSwitcher';
import { useInputActions, useInputState } from '@/hooks/useInputContext';

// The footer's left side: the account the thread runs on and its plan usage.
export function ComposerAccount() {
  const state = useInputState();
  const actions = useInputActions();
  if (!actions.onProfileChange) return null;

  return (
    <AccountSwitcher
      selectedProfileId={state.selectedProfileId ?? ''}
      onProfileChange={actions.onProfileChange}
      locked={state.profileLocked}
      fallbackProvider={state.agentKind}
      disabled={state.isLoading && !state.profileLocked}
    />
  );
}
