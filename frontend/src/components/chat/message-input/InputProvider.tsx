import { useRef, useState, useMemo, type ReactNode } from 'react';
import { useAuthStore } from '@/store/authStore';
import { useUIStore, type ComposerSelection } from '@/store/uiStore';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import { useRecentChatsQuery } from '@/hooks/queries/useChatQueries';
import { useChatContext } from '@/hooks/useChatContext';
import { useInputAttachments } from '@/hooks/useInputAttachments';
import { useInputEnhance } from '@/hooks/useInputEnhance';
import { useInputSuggestions } from '@/hooks/useInputSuggestions';
import { useInputSubmit } from '@/hooks/useInputSubmit';
import { useIsMobile } from '@/hooks/useIsMobile';
import {
  InputContext,
  InputStateContext,
  InputActionsContext,
  type InputState,
  type InputActions,
  type InputMeta,
  type InputContextValue,
} from './InputContext';
import type { InputProps } from './Input';
import type { Chat } from '@/types/chat.types';

// Stable fallback so chat-less composers (landing page) don't churn the selector.
const NO_SELECTIONS: ComposerSelection[] = [];
const NO_CHATS: Chat[] = [];

export function InputProvider({
  message,
  setMessage,
  onSubmit,
  onAttach,
  attachedFiles = null,
  isLoading,
  isStreaming = false,
  onStopStream,
  placeholder: placeholderProp,
  selectedModelId,
  onModelChange,
  dropdownPosition = 'top',
  showAttachedFilesPreview = true,
  contextUsage,
  showTip = true,
  showBranch = true,
  compact = true,
  chatId,
  showLoadingSpinner = false,
  disabled = false,
  children,
}: InputProps & { children: ReactNode }) {
  const { fileStructure, customSkills, builtinSlashCommands, personas } = useChatContext();
  // `/models/` is auth-protected; gate so the public landing composer doesn't 401.
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated);
  const modelMap = useModelMap(isAuthenticated);
  const { data: recentChats } = useRecentChatsQuery(isAuthenticated);
  const selectedModel = modelMap.get(selectedModelId);
  const storedSelections = useUIStore((s) =>
    chatId ? s.composerSelectionsByChat[chatId] : undefined,
  );
  const attachedSelections = storedSelections ?? NO_SELECTIONS;

  const formRef = useRef<HTMLFormElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [previewDismissed, setPreviewDismissed] = useState(false);
  const [cursorPosition, setCursorPosition] = useState(0);
  const messageRef = useRef(message);
  messageRef.current = message;
  // On a phone the @ and / hint wraps to a second line and makes the composer taller.
  const isMobile = useIsMobile();
  const placeholder =
    placeholderProp ??
    (isMobile ? 'Message Bitbox...' : 'Message Bitbox... (@ to mention, / for commands)');

  const hasMessage = message.trim().length > 0;
  // A selection chip is prompt content on its own — it can be sent without text.
  const hasContent = hasMessage || attachedSelections.length > 0;
  const hasAttachments = (attachedFiles?.length ?? 0) > 0;

  const prevHasAttachments = useRef(hasAttachments);
  if (prevHasAttachments.current !== hasAttachments) {
    prevHasAttachments.current = hasAttachments;
    setPreviewDismissed(false);
  }

  const showPreview = showAttachedFilesPreview && hasAttachments && !previewDismissed;

  const {
    previewUrls,
    showFileUpload,
    setShowFileUpload,
    showDrawingModal,
    editingImageIndex,
    handleFileSelect,
    handleAddFiles,
    handleRemoveFile,
    handleDrawClick,
    handleDrawingSave,
    closeDrawingModal,
    isDragging,
    dragHandlers,
    resetDragState,
    handlePaste,
  } = useInputAttachments({ attachedFiles, onAttach });

  const { isEnhancing, handleEnhancePrompt } = useInputEnhance({
    setMessage,
    textareaRef,
    selectedModelId,
    messageRef,
    hasMessage,
  });

  const {
    slashCommandSuggestions,
    highlightedSlashCommandIndex,
    selectSlashCommand,
    handleSlashCommandKeyDown,
    filteredFiles,
    filteredChats,
    highlightedMentionIndex,
    selectMention,
    handleMentionKeyDown,
    isMentionActive,
  } = useInputSuggestions({
    message,
    cursorPosition,
    setMessage,
    setCursorPosition,
    messageRef,
    textareaRef,
    fileStructure,
    mentionChats: recentChats?.items ?? NO_CHATS,
    chatId,
    customSkills,
    builtinSlashCommands,
  });

  const {
    handleSubmit,
    submitOrQueue,
    handleRemoveSelection,
    handleKeyDown,
    handleSendClick,
    handleStopClick,
  } = useInputSubmit({
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
  });

  const dynamicPlaceholder = isStreaming ? 'Type to queue message...' : placeholder;
  const canStop = !!onStopStream && (isStreaming || isLoading);

  const stateValue: InputState = useMemo(
    () => ({
      message,
      cursorPosition,
      isLoading,
      isDisabled: disabled,
      isStreaming,
      canStop,
      isEnhancing,
      hasMessage,
      hasContent,
      hasAttachments,
      showPreview,
      showFileUpload,
      showDrawingModal,
      showLoadingSpinner,
      showTip,
      showBranch,
      isDragging,
      compact,
      placeholder: dynamicPlaceholder,
      selectedModelId,
      dropdownPosition,
      attachedFiles,
      attachedSelections,
      previewUrls,
      editingImageIndex,
      contextUsage,
      chatId,
      isMentionActive,
      slashCommandSuggestions,
      highlightedSlashCommandIndex,
      filteredFiles,
      filteredChats,
      highlightedMentionIndex,
    }),
    [
      message,
      cursorPosition,
      isLoading,
      disabled,
      isStreaming,
      canStop,
      isEnhancing,
      hasMessage,
      hasContent,
      hasAttachments,
      showPreview,
      showFileUpload,
      showDrawingModal,
      showLoadingSpinner,
      showTip,
      showBranch,
      isDragging,
      compact,
      dynamicPlaceholder,
      selectedModelId,
      dropdownPosition,
      attachedFiles,
      attachedSelections,
      previewUrls,
      editingImageIndex,
      contextUsage,
      chatId,
      isMentionActive,
      slashCommandSuggestions,
      highlightedSlashCommandIndex,
      filteredFiles,
      filteredChats,
      highlightedMentionIndex,
    ],
  );

  const actionsValue: InputActions = useMemo(
    () => ({
      setMessage,
      setCursorPosition,
      setShowFileUpload,
      onModelChange,
      handleSubmit,
      submitOrQueue,
      handleKeyDown,
      handleSendClick,
      handleStopClick,
      handleEnhancePrompt,
      handleFileSelect,
      handleAddFiles,
      handleRemoveFile,
      handleRemoveSelection,
      handleDrawClick,
      handleDrawingSave,
      handlePaste,
      closeDrawingModal,
      resetDragState,
      selectSlashCommand,
      selectMention,
    }),
    [
      setMessage,
      setCursorPosition,
      setShowFileUpload,
      onModelChange,
      handleSubmit,
      submitOrQueue,
      handleKeyDown,
      handleSendClick,
      handleStopClick,
      handleEnhancePrompt,
      handleFileSelect,
      handleAddFiles,
      handleRemoveFile,
      handleRemoveSelection,
      handleDrawClick,
      handleDrawingSave,
      handlePaste,
      closeDrawingModal,
      resetDragState,
      selectSlashCommand,
      selectMention,
    ],
  );

  const metaValue: InputMeta = useMemo(
    () => ({
      formRef,
      textareaRef,
      dragHandlers,
    }),
    [dragHandlers],
  );

  const value: InputContextValue = useMemo(
    () => ({ state: stateValue, actions: actionsValue, meta: metaValue }),
    [stateValue, actionsValue, metaValue],
  );

  return (
    <InputContext.Provider value={value}>
      <InputStateContext.Provider value={stateValue}>
        <InputActionsContext.Provider value={actionsValue}>{children}</InputActionsContext.Provider>
      </InputStateContext.Provider>
    </InputContext.Provider>
  );
}
