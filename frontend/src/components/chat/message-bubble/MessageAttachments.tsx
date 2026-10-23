import { memo } from 'react';
import { AttachmentViewer } from '@/components/ui/attachment-viewer/AttachmentViewer';
import type { MessageAttachment } from '@/types/chat.types';

interface MessageAttachmentsProps {
  attachments: MessageAttachment[];
  uploadingAttachmentIds?: string[];
  className?: string;
}

export const MessageAttachments = memo(function MessageAttachments({
  attachments,
  uploadingAttachmentIds,
  className = '',
}: MessageAttachmentsProps) {
  if (attachments.length === 0) {
    return null;
  }

  return (
    <div className={className}>
      <AttachmentViewer attachments={attachments} uploadingAttachmentIds={uploadingAttachmentIds} />
    </div>
  );
});
