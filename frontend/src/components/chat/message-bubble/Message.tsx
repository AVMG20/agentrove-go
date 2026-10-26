import { memo, useMemo, useState, type ReactNode } from 'react';
import { Undo2 } from 'lucide-react';
import { UserMessageContent, AssistantMessageContent } from './MessageContent';
import { MessageActions } from './MessageActions';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import type { AssistantStreamEvent, MessageAttachment } from '@/types/chat.types';
import { Tooltip } from '@/components/ui/Tooltip/Tooltip';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { formatRelativeTime, formatFullTimestamp } from '@/utils/date';
import { useChatContext } from '@/hooks/useChatContext';
import { useCheckpointRestore } from '@/hooks/useCheckpointRestore';
import styles from './Message.module.scss';

function MessageRow({ children, mine }: { children: ReactNode; mine?: boolean }) {
  return (
    <div className={mine ? `${styles.message} ${styles.mine}` : styles.message}>
      <div className={styles['message-row']}>
        <div className={styles['message-body']}>{children}</div>
      </div>
    </div>
  );
}

function MessageText({ children }: { children: ReactNode }) {
  return <div className={styles['message-text']}>{children}</div>;
}

export function UserBubble({ children }: { children: ReactNode }) {
  return (
    <div className={styles['user-bubble']}>
      <MessageText>{children}</MessageText>
    </div>
  );
}

interface SharedContentProps {
  contentRender: {
    events: AssistantStreamEvent[];
  };
  attachments: MessageAttachment[];
  isStreaming: boolean;
}

export interface UserMessageProps extends SharedContentProps {
  id: string;
  contentText: string;
  uploadingAttachmentIds?: string[];
}

export const UserMessage = memo(function UserMessage({
  id,
  contentText,
  contentRender,
  attachments,
  uploadingAttachmentIds,
  isStreaming,
}: UserMessageProps) {
  const { chatId } = useChatContext();

  return (
    <MessageRow mine>
      <UserBubble>
        <UserMessageContent
          contentRender={contentRender}
          attachments={attachments}
          uploadingAttachmentIds={uploadingAttachmentIds}
          isStreaming={isStreaming}
          chatId={chatId}
        />
      </UserBubble>

      {contentText.trim() && !isStreaming && (
        <div className={styles['actions-slot']}>
          <MessageActions messageId={id} contentText={contentText} />
        </div>
      )}
    </MessageRow>
  );
});

export interface AssistantMessageProps extends SharedContentProps {
  contentText: string;
  id: string;
  checkpointId: string | null;
  createdAt?: string;
  modelId?: string;
  durationMs?: number | null;
  toolResultsOmitted: boolean;
}

export const AssistantMessage = memo(function AssistantMessage({
  id,
  checkpointId,
  contentText,
  contentRender,
  attachments,
  isStreaming,
  createdAt,
  modelId,
  durationMs,
  toolResultsOmitted,
}: AssistantMessageProps) {
  const { chatId, sandboxId } = useChatContext();
  const modelMap = useModelMap();
  const [restoreOpen, setRestoreOpen] = useState(false);
  const { restore, isRestoring } = useCheckpointRestore(id, sandboxId);

  const relativeTime = createdAt ? formatRelativeTime(createdAt) : '';
  const fullTimestamp = createdAt ? formatFullTimestamp(createdAt) : '';
  const modelName = useMemo(() => {
    if (!modelId) return null;
    return modelMap.get(modelId)?.name ?? modelId;
  }, [modelId, modelMap]);

  const omittedToolResults = useMemo(
    () => (toolResultsOmitted ? { messageId: id } : undefined),
    [toolResultsOmitted, id],
  );
  const hasContentText = contentText.trim().length > 0;
  const showFooter = (hasContentText || checkpointId != null) && !isStreaming;

  return (
    <MessageRow>
      <MessageText>
        <AssistantMessageContent
          contentRender={contentRender}
          attachments={attachments}
          isStreaming={isStreaming}
          chatId={chatId}
          durationMs={durationMs}
          omittedToolResults={omittedToolResults}
        />
      </MessageText>

      {showFooter && (
        <div className={styles.footer}>
          <div className={styles['footer-actions']}>
            {hasContentText && <MessageActions messageId={id} contentText={contentText} />}
            {checkpointId && (
              <Tooltip content="Restore to before this run" position="bottom">
                <Button
                  onClick={() => setRestoreOpen(true)}
                  variant="unstyled"
                  disabled={isRestoring}
                  aria-label="Restore to before this run"
                  className={styles['restore-button']}
                >
                  <Undo2 className={styles['restore-icon']} />
                </Button>
              </Tooltip>
            )}
          </div>

          <div className={styles['footer-meta']}>
            {modelName && <span>{modelName}</span>}
            {modelName && relativeTime && <span>·</span>}
            {relativeTime && (
              <Tooltip content={fullTimestamp} position="bottom">
                <span className={styles['footer-time']}>{relativeTime}</span>
              </Tooltip>
            )}
          </div>
        </div>
      )}
      {restoreOpen && (
        <ConfirmDialog
          isOpen
          onClose={() => setRestoreOpen(false)}
          onConfirm={() => restore()}
          title="Restore to before this run?"
          message="The project will be reset to the checkpoint captured before this assistant run. Changes made by the run will be discarded."
          confirmLabel="Restore"
        />
      )}
    </MessageRow>
  );
});
