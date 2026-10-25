import { PermissionModeSelector } from '@/components/chat/permission-mode-selector/PermissionModeSelector';
import { ModelSelector } from '@/components/chat/model-selector/ModelSelector';
import { ProfileSelector } from '@/components/chat/profile-selector/ProfileSelector';
import { ThinkingModeSelector } from '@/components/chat/thinking-mode-selector/ThinkingModeSelector';
import { getThinkingModes } from '@/components/chat/thinking-mode-selector/thinkingModes';
import { PersonaSelector } from '@/components/chat/persona-selector/PersonaSelector';
import { BranchSelector } from '@/components/chat/branch-selector/BranchSelector';
import { useInputState, useInputActions } from '@/hooks/useInputContext';
import { useAuthStore } from '@/store/authStore';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import { useChatContext } from '@/hooks/useChatContext';
import { useGitBranchesQuery } from '@/hooks/queries/useSandboxQueries';
import { SelectorDot } from '@/components/ui/primitives/SelectorDot/SelectorDot';
import styles from './InputControls.module.scss';

export function InputControls() {
  const state = useInputState();
  const actions = useInputActions();
  // `/models/` is auth-protected; gate so the public landing composer doesn't 401.
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const modelMap = useModelMap(isAuthenticated);
  const selectedModel = modelMap.get(state.selectedModelId);

  const { sandboxId, worktreeCwd, personas } = useChatContext();
  const { data: branchesData } = useGitBranchesQuery(sandboxId, !!sandboxId, worktreeCwd);

  // Permission modes and personas are Claude Code features.
  const isClaude = state.agentKind === 'claude';
  const showPersona = isClaude && personas.length > 0;
  const showBranch =
    state.showBranch &&
    !!sandboxId &&
    !!branchesData?.is_git_repo &&
    branchesData.branches.length > 0;
  // Model-aware: some models expose no reasoning dial (e.g. Haiku). Show it
  // until the model list loads.
  const showThinking = selectedModel ? getThinkingModes(selectedModel).length > 0 : true;

  return (
    <div className={styles['input-controls']} onMouseDown={(e) => e.preventDefault()}>
      {actions.onProfileChange && state.selectedProfileId && (
        <>
          <ProfileSelector
            selectedProfileId={state.selectedProfileId}
            onProfileChange={actions.onProfileChange}
            locked={state.profileLocked}
            fallbackProvider={state.agentKind}
            dropdownPosition={state.dropdownPosition}
            dropdownAlign="right"
            disabled={state.isLoading && !state.profileLocked}
            variant="text"
          />
          <SelectorDot />
        </>
      )}

      <ModelSelector
        selectedModelId={state.selectedModelId}
        onModelChange={actions.onModelChange}
        agentKind={state.agentKind}
        dropdownPosition={state.dropdownPosition}
        dropdownAlign="right"
        disabled={state.isLoading}
        variant="text"
      />

      {showThinking && (
        <>
          <SelectorDot />

          <ThinkingModeSelector
            chatId={state.chatId}
            model={selectedModel}
            dropdownPosition={state.dropdownPosition}
            dropdownAlign="right"
            disabled={state.isLoading}
            variant="text"
          />
        </>
      )}

      {isClaude && (
        <>
          <SelectorDot />

          <PermissionModeSelector
            chatId={state.chatId}
            dropdownPosition={state.dropdownPosition}
            dropdownAlign="right"
            disabled={state.isLoading}
            variant="text"
          />
        </>
      )}

      {showPersona && (
        <>
          <SelectorDot />
          <PersonaSelector
            chatId={state.chatId}
            dropdownPosition={state.dropdownPosition}
            dropdownAlign="right"
            disabled={state.isLoading}
            variant="text"
          />
        </>
      )}

      {showBranch && (
        <>
          <SelectorDot />
          <BranchSelector
            dropdownPosition={state.dropdownPosition}
            dropdownAlign="right"
            disabled={state.isLoading}
            variant="text"
          />
        </>
      )}
    </div>
  );
}
