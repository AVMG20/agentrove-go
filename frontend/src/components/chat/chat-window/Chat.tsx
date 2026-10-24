import { useCallback, useEffect, memo, useMemo } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useShallow } from 'zustand/react/shallow';
import { isBrowserObjectUrl } from '@/utils/attachmentUrl';
import { isAssistantMessage } from '@/utils/message';
import type { AssistantStreamEvent } from '@/types/chat.types';
import { UserMessage, AssistantMessage } from '@/components/chat/message-bubble/Message';
import { ChatQueueBanner } from './ChatQueueBanner';
import { BackgroundTasksTray } from './BackgroundTasksTray';
import { ChatInlinePermission } from './ChatInlinePermission';
import { ChatInlineElicitation } from './ChatInlineElicitation';
import { StreamActionsBar } from './StreamActionsBar';
import { Input } from '@/components/chat/message-input/Input';
import { ChatSkeleton } from './ChatSkeleton';
import { ConversationColumn, ConversationLayout, ConversationScroller } from './ConversationLayout';
import { MessageTrail } from './MessageTrail';
import { FindInChat } from './FindInChat';
import { ChatSelectionActions } from './ChatSelectionActions';
import { StatusTypewriter } from './StatusTypewriter';
import { StreamWorkingStatus } from './StreamWorkingStatus';
import { TurnStatusFooter } from './TurnStatusFooter';
import { useChatScroll } from './useChatScroll';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { useStreamStore } from '@/store/streamStore';
import { useMessageQueueStore, EMPTY_QUEUE } from '@/store/messageQueueStore';
import { useChatContext } from '@/hooks/useChatContext';
import { useChatSessionContext } from '@/hooks/useChatSessionContext';
import { useChatInputMessageContext } from '@/hooks/useChatInputMessageContext';
import { useUIStore } from '@/store/uiStore';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { retryQueuedMessage } from '@/utils/queuedMessageFailure';
import styles from './Chat.module.scss';

// Whether a tool is still running in a streaming reply, plus a key that changes
// whenever the reply grows (new event, or more text in the tail one).
function streamActivity(events: AssistantStreamEvent[] | undefined): {
  toolRunning: boolean;
  activityKey: string;
} {
  if (!events?.length) return { toolRunning: false, activityKey: '0' };
  const running = new Set<string>();
  for (const event of events) {
    if (event.type === 'tool_started') running.add(event.tool.id);
    else if (event.type === 'tool_completed' || event.type === 'tool_failed') {
      running.delete(event.tool.id);
    }
  }
  const tail = events[events.length - 1];
  const tailSize =
    tail.type === 'assistant_text'
      ? tail.text.length
      : tail.type === 'assistant_thinking'
        ? tail.thinking.length
        : 0;
  return { toolRunning: running.size > 0, activityKey: `${events.length}:${tailSize}` };
}

export const Chat = memo(function Chat() {
  const { chatId } = useChatContext();
  const { state, actions } = useChatSessionContext();
  const queryClient = useQueryClient();

  const {
    messages,
    pendingUserMessageId,
    isLoading,
    isStreaming,
    isInitialLoading,
    attachedFiles,
    selectedModelId,
    contextUsage,
    hasNextPage,
    isFetchingNextPage,
    pendingPermissionRequest,
    pendingElicitationRequest,
  } = state;

  const { onSubmit, onStopStream, onSendPrompt, onAttach, onModelChange, fetchNextPage } = actions;

  const { inputMessage, setInputMessage } = useChatInputMessageContext();

  useEffect(() => {
    return useUIStore.subscribe((state, prev) => {
      if (
        state.pendingChatMessage &&
        state.pendingChatMessage !== prev.pendingChatMessage &&
        state.pendingChatMessage.chatId === chatId
      ) {
        setInputMessage(state.pendingChatMessage.message);
        useUIStore.getState().setPendingChatMessage(null);
      }
    });
  }, [chatId, setInputMessage]);

  const { activeStreams, streamIdByChatMessage } = useStreamStore(
    useShallow((s) => ({
      activeStreams: s.activeStreams,
      streamIdByChatMessage: s.streamIdByChatMessage,
    })),
  );
  const streamingMessageIdSet = useMemo(() => {
    const ids = new Set<string>();
    if (!chatId) return ids;

    for (const streamId of streamIdByChatMessage.values()) {
      const stream = activeStreams.get(streamId);
      if (stream?.chatId === chatId && stream.isActive) {
        ids.add(stream.messageId);
      }
    }

    return ids;
  }, [activeStreams, chatId, streamIdByChatMessage]);

  // Real start time of the active stream for this chat, so the thinking timer
  // doesn't reset when switching chats and the indicator remounts.
  const streamStartTime = useStreamStore((s) =>
    chatId ? s.activeStreamMetadata.find((m) => m.chatId === chatId)?.startTime : undefined,
  );

  const pendingMessages = useMessageQueueStore((storeState) =>
    chatId ? (storeState.queues.get(chatId) ?? EMPTY_QUEUE) : EMPTY_QUEUE,
  );

  useEffect(() => {
    if (chatId) {
      void useMessageQueueStore.getState().fetchQueue(chatId);
    }
  }, [chatId]);

  const handleCancelMessage = useCallback(
    (messageId: string) => {
      if (chatId) {
        void useMessageQueueStore.getState().removeMessage(chatId, messageId);
      }
    },
    [chatId],
  );

  const handleEditMessage = useCallback(
    (messageId: string, newContent: string) => {
      if (chatId) {
        void useMessageQueueStore.getState().updateQueuedMessage(chatId, messageId, newContent);
      }
    },
    [chatId],
  );

  const handleSendNow = useCallback(
    async (messageId: string) => {
      if (!chatId) return;
      const success = await useMessageQueueStore.getState().sendNow(chatId, messageId);
      if (success && !isStreaming) {
        useMessageQueueStore.getState().removeLocalOnly(chatId, messageId);
        await queryClient.invalidateQueries({ queryKey: queryKeys.messages(chatId) });
      }
    },
    [chatId, isStreaming, queryClient],
  );

  const handleRetryQueued = useCallback(
    (messageId: string) => {
      if (chatId) retryQueuedMessage(chatId, messageId);
    },
    [chatId],
  );

  const { lastBotMessage, latestUserMessageId } = useMemo(() => {
    let latestAssistantMessage: (typeof messages)[number] | undefined;
    let latestUserId: string | null = null;

    for (let i = messages.length - 1; i >= 0; i--) {
      const message = messages[i];
      const isAssistant = isAssistantMessage(message);

      if (!latestAssistantMessage && isAssistant) {
        latestAssistantMessage = message;
      }

      if (latestUserId === null && !isAssistant) {
        latestUserId = message.id;
      }

      if (latestAssistantMessage && latestUserId !== null) {
        break;
      }
    }

    return {
      lastBotMessage: latestAssistantMessage,
      latestUserMessageId: latestUserId,
    };
  }, [messages]);

  const lastBotMessageId = lastBotMessage?.id ?? null;

  const {
    scrollerRef,
    turnRef,
    turnMinHeight,
    showScrollButton,
    containerRefCallback,
    scrollToBottom,
  } = useChatScroll({
    chatId,
    messages,
    pendingUserMessageId,
    latestUserMessageId,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  });

  const turns = useMemo(() => {
    // Group each user message with the assistant output that follows so the last
    // turn can carry the send-anchor min-height without reparenting rows mid-stream.
    const groups: (typeof messages)[] = [];
    for (const msg of messages) {
      if (isAssistantMessage(msg) && groups.length > 0) {
        groups[groups.length - 1].push(msg);
      } else {
        groups.push([msg]);
      }
    }
    return groups;
  }, [messages]);

  const canShowPermissionInline =
    pendingPermissionRequest && pendingPermissionRequest.tool_name !== 'ExitPlanMode';
  const lastBotIsStreaming = !!lastBotMessageId && streamingMessageIdSet.has(lastBotMessageId);
  const lastBotHasContent =
    !!lastBotMessage &&
    ((lastBotMessage.content_render?.events?.length ?? 0) > 0 || !!lastBotMessage.content_text);
  const showPermissionAtEnd = canShowPermissionInline && (!lastBotMessageId || lastBotIsStreaming);
  // Permission and elicitation share the slot; a pending permission wins.
  const canShowElicitationInline = !!pendingElicitationRequest && !pendingPermissionRequest;
  const showElicitationAtEnd =
    canShowElicitationInline && (!lastBotMessageId || lastBotIsStreaming);
  // Only offer actions against a cleanly completed turn — skips failed/interrupted
  // output and the reconnect window where an in-progress message isn't yet in the stream set.
  const showStreamActions =
    lastBotMessage?.stream_status === 'completed' &&
    !lastBotIsStreaming &&
    !isStreaming &&
    !isLoading;
  // A stopped/failed/cut-off turn that ends the conversation gets a status footer.
  const unfinishedStatus =
    lastBotMessage?.stream_status === 'failed' || lastBotMessage?.stream_status === 'interrupted'
      ? lastBotMessage.stream_status
      : null;
  const showTurnStatus =
    !!unfinishedStatus &&
    messages[messages.length - 1]?.id === lastBotMessageId &&
    !lastBotIsStreaming &&
    !isStreaming &&
    !isLoading;
  const latestUserText = useMemo(
    () => messages.find((msg) => msg.id === latestUserMessageId)?.content_text.trim() ?? '',
    [messages, latestUserMessageId],
  );
  const handleRetryTurn = useCallback(
    () => onSendPrompt(latestUserText),
    [latestUserText, onSendPrompt],
  );
  const handleContinueTurn = useCallback(() => onSendPrompt('Continue'), [onSendPrompt]);

  // Streaming reply that has gone quiet: show "still working" unless a tool card
  // (or a prompt for the user) already says what's happening.
  const lastBotEvents = lastBotMessage?.content_render?.events;
  const { toolRunning, activityKey } = useMemo(
    () => streamActivity(lastBotEvents),
    [lastBotEvents],
  );

  const renderMessage = useCallback(
    (msg: (typeof messages)[number]) => {
      const messageIsStreaming = streamingMessageIdSet.has(msg.id);
      const isBotMessage = isAssistantMessage(msg);
      const isLastBotMessage = isBotMessage && msg.id === lastBotMessageId;
      // A not-yet-populated bot row renders only its padding, pushing the thinking
      // indicator below it down a few pixels — hide it until content lands. Gate on the
      // turn being active, not streamingMessageIdSet: the row can land in the message
      // list before the stream store registers its message id.
      if (isLastBotMessage && !lastBotHasContent && (isLoading || isStreaming)) {
        return null;
      }
      const attachments = msg.attachments ?? [];
      const localAttachmentIds = attachments.reduce<string[]>((acc, attachment) => {
        if (isBrowserObjectUrl(attachment.file_url)) acc.push(attachment.id);
        return acc;
      }, []);
      const isLatestUserMessage = !isBotMessage && msg.id === latestUserMessageId;
      const shouldShowUploadingOverlay =
        localAttachmentIds.length > 0 &&
        (pendingUserMessageId === msg.id ||
          (isLatestUserMessage && (pendingUserMessageId !== null || isLoading)));
      const uploadingAttachmentIds = shouldShowUploadingOverlay ? localAttachmentIds : undefined;

      return (
        <ConversationColumn>
          {isBotMessage ? (
            <AssistantMessage
              id={msg.id}
              contentText={msg.content_text}
              contentRender={msg.content_render}
              attachments={attachments}
              isStreaming={messageIsStreaming}
              createdAt={msg.created_at}
              modelId={msg.model_id ?? undefined}
              durationMs={msg.duration_ms}
              checkpointId={msg.checkpoint_id}
              toolResultsOmitted={msg.tool_results_omitted}
              // Idle-gated so prompt suggestions unmount in the send commit itself —
              // unmounting when the placeholder lands shifts the anchored turn late.
              isLastBotMessage={
                isLastBotMessage && !messageIsStreaming && !isLoading && !isStreaming
              }
            />
          ) : (
            <UserMessage
              id={msg.id}
              contentText={msg.content_text}
              contentRender={msg.content_render}
              attachments={attachments}
              uploadingAttachmentIds={uploadingAttachmentIds}
              isStreaming={messageIsStreaming}
            />
          )}
          {isLastBotMessage && !messageIsStreaming && (
            <>
              <ChatInlinePermission />
              <ChatInlineElicitation />
            </>
          )}
        </ConversationColumn>
      );
    },
    [
      isLoading,
      isStreaming,
      lastBotHasContent,
      lastBotMessageId,
      latestUserMessageId,
      pendingUserMessageId,
      streamingMessageIdSet,
    ],
  );

  const listHeader = useMemo(() => {
    if (!hasNextPage) {
      return null;
    }

    return (
      <ConversationColumn>
        <div className={styles['list-header-row']}>
          {isFetchingNextPage && (
            <div className={styles['loading-more']}>
              <Spinner size="xs" />
              Loading older messages...
            </div>
          )}
        </div>
      </ConversationColumn>
    );
  }, [hasNextPage, isFetchingNextPage]);

  // !lastBotIsStreaming covers the follow-up-turn handoff: the previous turn's
  // message (with content) is still the last bot row until the new stream's message
  // lands in the list — without it the indicator unmounts/remounts, restarting the
  // typewriter at "Pondering".
  const showThinking = isLoading || (isStreaming && (!lastBotIsStreaming || !lastBotHasContent));
  const showWorking =
    !showThinking &&
    isStreaming &&
    lastBotIsStreaming &&
    !toolRunning &&
    !showPermissionAtEnd &&
    !showElicitationAtEnd;

  const listFooter = useMemo(() => {
    if (
      !showThinking &&
      !showWorking &&
      !showPermissionAtEnd &&
      !showElicitationAtEnd &&
      !showTurnStatus &&
      !(showStreamActions && chatId)
    ) {
      return null;
    }

    return (
      <ConversationColumn>
        {showThinking && <StatusTypewriter streamStartTime={streamStartTime} />}
        {showWorking && (
          <StreamWorkingStatus streamStartTime={streamStartTime} activityKey={activityKey} />
        )}
        {showPermissionAtEnd && <ChatInlinePermission />}
        {showElicitationAtEnd && <ChatInlineElicitation />}
        {showTurnStatus && unfinishedStatus && (
          <TurnStatusFooter
            status={unfinishedStatus}
            error={lastBotMessage?.stream_error}
            onRetry={latestUserText ? handleRetryTurn : undefined}
            onContinue={handleContinueTurn}
          />
        )}
        {showStreamActions && chatId && <StreamActionsBar chatId={chatId} />}
      </ConversationColumn>
    );
  }, [
    activityKey,
    chatId,
    handleContinueTurn,
    handleRetryTurn,
    lastBotMessage?.stream_error,
    latestUserText,
    showElicitationAtEnd,
    showPermissionAtEnd,
    showStreamActions,
    showThinking,
    showTurnStatus,
    showWorking,
    streamStartTime,
    unfinishedStatus,
  ]);

  return (
    <ConversationLayout
      showScrollButton={showScrollButton}
      onScrollToBottom={scrollToBottom}
      composerBanner={
        <>
          {chatId && <BackgroundTasksTray chatId={chatId} onStopTurn={onStopStream} />}
          <ChatQueueBanner
            messages={pendingMessages}
            onCancel={handleCancelMessage}
            onEdit={handleEditMessage}
            onSendNow={handleSendNow}
            onRetry={handleRetryQueued}
          />
        </>
      }
      composer={
        <Input
          message={inputMessage}
          setMessage={setInputMessage}
          onSubmit={onSubmit}
          onAttach={onAttach}
          attachedFiles={attachedFiles}
          isLoading={isLoading}
          isStreaming={isStreaming}
          onStopStream={onStopStream}
          selectedModelId={selectedModelId}
          onModelChange={onModelChange}
          dropdownPosition="top"
          showAttachedFilesPreview={true}
          contextUsage={contextUsage}
          showTip={false}
          chatId={chatId}
        />
      }
    >
      {isInitialLoading && messages.length === 0 ? (
        <ChatSkeleton messageCount={3} />
      ) : (
        <>
          {/* Single wrapper so the stick-to-bottom ResizeObserver tracks all content */}
          <ConversationScroller key={chatId ?? 'chat'} ref={containerRefCallback}>
            {listHeader}

            {turns.map((turn, turnIndex) => {
              const isLastTurn = turnIndex === turns.length - 1;
              return (
                <div
                  key={turn[0].id}
                  ref={isLastTurn ? turnRef : undefined}
                  style={isLastTurn && turnMinHeight > 0 ? { minHeight: turnMinHeight } : undefined}
                >
                  {turn.map((msg) => (
                    <div key={msg.id} data-message-id={msg.id}>
                      {renderMessage(msg)}
                    </div>
                  ))}
                  {isLastTurn && listFooter}
                </div>
              );
            })}

            {turns.length === 0 && listFooter}

            {/* Inside the content wrapper so the selection toolbar / ask
                panel anchor in content coordinates and scroll with it */}
            <ChatSelectionActions chatId={chatId} scrollerRef={scrollerRef} />
          </ConversationScroller>
          <MessageTrail messages={messages} scrollerRef={scrollerRef} />
          {/* Keyed by chat so query/match state resets on chat switch (prefixed: the
              scroller beside it is keyed by chat too, and sibling keys must differ) */}
          <FindInChat
            key={`find-${chatId ?? 'chat'}`}
            messages={messages}
            scrollerRef={scrollerRef}
          />
        </>
      )}
    </ConversationLayout>
  );
});
