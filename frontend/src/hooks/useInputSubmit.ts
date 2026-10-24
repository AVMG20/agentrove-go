import { useCallback, type RefObject } from 'react';
import toast from 'react-hot-toast';
import { useMessageQueueStore } from '@/store/messageQueueStore';
import { useUIStore, type ComposerSelection } from '@/store/uiStore';
import { formatComposerSelections } from '@/utils/composerSelections';
import { coercePermissionMode } from '@/components/chat/permission-mode-selector/permissionModes';
import { coerceThinkingMode } from '@/components/chat/thinking-mode-selector/thinkingModes';
import {
  useChatSettingsStore,
  DEFAULT_PERMISSION_MODE,
  DEFAULT_THINKING_MODE,
  DEFAULT_WORKTREE,
  DEFAULT_PERSONA,
} from '@/store/chatSettingsStore';
import { resolvePersona } from '@/utils/settings';
import { useTokenDeleteKeyDown } from '@/hooks/useComposerTokens';
import { isComposerSendKey } from '@/utils/composerKeys';
import type { Persona } from '@/types/user.types';
import type { Model } from '@/types/chat.types';

interface UseInputSubmitOptions {
  disabled: boolean;
  hasContent: boolean;
  onSubmit: (e: React.FormEvent) => void;
  setPreviewDismissed: React.Dispatch<React.SetStateAction<boolean>>;
  isStreaming: boolean;
  onStopStream?: () => void;
  isLoading: boolean;
  chatId?: string;
  selectedModelId: string;
  selectedModel: Model | undefined;
  personas: Persona[];
  attachedSelections: ComposerSelection[];
  attachedFiles: File[] | null;
  messageRef: RefObject<string>;
  setMessage: (value: string) => void;
  onAttach?: (files: File[]) => void;
  formRef: RefObject<HTMLFormElement | null>;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  handleMentionKeyDown: (e: React.KeyboardEvent<Element>) => boolean;
  handleSlashCommandKeyDown: (e: React.KeyboardEvent<Element>) => boolean;
}

// Submit/queue/stop logic plus the keyboard wiring around it — Enter-to-send,
// Esc-to-stop, suggestion-aware key handling, token-pill deletion, and selection
// removal. Sending never stops a turn: an empty Enter is a no-op, so a stray
// Return can't kill a running agent; Esc and the Stop button do that.
export function useInputSubmit({
  disabled,
  hasContent,
  onSubmit,
  setPreviewDismissed,
  isStreaming,
  onStopStream,
  isLoading,
  chatId,
  selectedModelId,
  selectedModel,
  personas,
  attachedSelections,
  attachedFiles,
  messageRef,
  setMessage,
  onAttach,
  formRef,
  textareaRef,
  handleMentionKeyDown,
  handleSlashCommandKeyDown,
}: UseInputSubmitOptions) {
  const handleSubmit = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      if (disabled) return;
      if (!hasContent) return;

      setPreviewDismissed(true);
      onSubmit(event);
    },
    [disabled, hasContent, onSubmit, setPreviewDismissed],
  );

  const submitOrQueue = useCallback(() => {
    if (disabled || !hasContent) return;

    if (isStreaming && chatId) {
      // Queue + clear draft — guard model or the message is lost.
      if (!selectedModelId) {
        toast.error('Please select an AI model');
        return;
      }
      const settings = useChatSettingsStore.getState();
      const permissionMode = coercePermissionMode(
        settings.permissionModeByChat[chatId] ?? DEFAULT_PERMISSION_MODE,
      );
      const thinkingMode = coerceThinkingMode(
        settings.thinkingModeByChat[chatId] ?? DEFAULT_THINKING_MODE,
        selectedModel,
      );
      const worktree = settings.worktreeByChat[chatId] ?? DEFAULT_WORKTREE;
      const baseBranch = settings.worktreeBaseBranchByChat[chatId];
      const storedPersona = settings.personaByChat[chatId] ?? DEFAULT_PERSONA;
      const validPersona = resolvePersona(storedPersona, personas);
      const fullMessage = formatComposerSelections(attachedSelections, messageRef.current.trim());
      void useMessageQueueStore.getState().queueMessage(chatId, fullMessage, selectedModelId, {
        permissionMode,
        thinkingMode,
        worktree,
        baseBranch,
        selectedPersonaName: validPersona,
        files: attachedFiles ?? undefined,
      });
      setMessage('');
      onAttach?.([]);
      if (attachedSelections.length > 0) useUIStore.getState().clearComposerSelections(chatId);
      setPreviewDismissed(true);
      return;
    }

    // The previous send is still starting; Stop cancels it.
    if (isLoading) return;

    setPreviewDismissed(true);

    const formElement = formRef.current;
    if (formElement && typeof formElement.requestSubmit === 'function') {
      formElement.requestSubmit();
      return;
    }

    const formEvent = new Event('submit', {
      bubbles: true,
      cancelable: true,
    }) as unknown as React.FormEvent;
    onSubmit(formEvent);
  }, [
    disabled,
    hasContent,
    isLoading,
    isStreaming,
    onSubmit,
    chatId,
    attachedFiles,
    attachedSelections,
    setMessage,
    onAttach,
    selectedModelId,
    selectedModel,
    personas,
    formRef,
    messageRef,
    setPreviewDismissed,
  ]);

  const stop = useCallback(() => {
    if (isStreaming || isLoading) onStopStream?.();
  }, [isLoading, isStreaming, onStopStream]);

  const handleRemoveSelection = useCallback(
    (index: number) => {
      if (chatId) useUIStore.getState().removeComposerSelection(chatId, index);
    },
    [chatId],
  );

  const handleTokenDeleteKeyDown = useTokenDeleteKeyDown(messageRef, textareaRef);

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent<Element>) => {
      const handledByMentions = handleMentionKeyDown(event);
      if (handledByMentions) return;

      const handledBySlashCommands = handleSlashCommandKeyDown(event);
      if (handledBySlashCommands) return;

      if (handleTokenDeleteKeyDown(event)) return;

      if (event.key === 'Escape') {
        // Popups and dialogs that own Esc handle it first (and mark it handled).
        if (event.defaultPrevented || event.nativeEvent?.isComposing) return;
        if (!isStreaming && !isLoading) return;
        if (document.querySelector('[aria-modal="true"]')) return;
        event.preventDefault();
        stop();
        return;
      }

      if (isComposerSendKey(event)) {
        event.preventDefault();
        submitOrQueue();
      }
    },
    [
      handleMentionKeyDown,
      handleSlashCommandKeyDown,
      handleTokenDeleteKeyDown,
      isLoading,
      isStreaming,
      stop,
      submitOrQueue,
    ],
  );

  const handleSendClick = useCallback(
    (event: React.MouseEvent) => {
      event.preventDefault();
      submitOrQueue();
    },
    [submitOrQueue],
  );

  const handleStopClick = useCallback(
    (event: React.MouseEvent) => {
      event.preventDefault();
      stop();
    },
    [stop],
  );

  return {
    handleSubmit,
    submitOrQueue,
    handleRemoveSelection,
    handleKeyDown,
    handleSendClick,
    handleStopClick,
  };
}
