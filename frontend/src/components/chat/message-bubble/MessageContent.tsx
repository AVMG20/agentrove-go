import { memo } from 'react';
import { MessageRenderer } from './MessageRenderer';
import type { AssistantStreamEvent, MessageAttachment } from '@/types/chat.types';
import { MessageAttachments } from './MessageAttachments';
import styles from './MessageContent.module.scss';

interface SharedContentProps {
  contentRender: {
    events: AssistantStreamEvent[];
  };
  attachments: MessageAttachment[];
  isStreaming: boolean;
  chatId?: string;
}

interface UserMessageContentProps extends SharedContentProps {
  uploadingAttachmentIds?: string[];
}

export const UserMessageContent = memo(function UserMessageContent({
  contentRender,
  attachments,
  uploadingAttachmentIds,
  isStreaming,
  chatId,
}: UserMessageContentProps) {
  return (
    <div className={styles['user-content']}>
      <MessageAttachments
        attachments={attachments}
        uploadingAttachmentIds={uploadingAttachmentIds}
      />
      <MessageRenderer
        events={contentRender.events}
        isStreaming={isStreaming}
        chatId={chatId}
        highlightMentions
      />
    </div>
  );
});

interface AssistantMessageContentProps extends SharedContentProps {
  omittedToolResults?: { messageId: string };
  durationMs?: number | null;
}

export const AssistantMessageContent = memo(function AssistantMessageContent({
  contentRender,
  attachments,
  isStreaming,
  chatId,
  durationMs,
  omittedToolResults,
}: AssistantMessageContentProps) {
  return (
    <div className={styles['assistant-content']}>
      <MessageRenderer
        events={contentRender.events}
        omittedToolResults={omittedToolResults}
        isStreaming={isStreaming}
        chatId={chatId}
        durationMs={durationMs}
      />

      <MessageAttachments attachments={attachments} className={styles.attachments} />
    </div>
  );
});
