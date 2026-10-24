import toast from 'react-hot-toast';
import { useComposerDraftStore } from '@/store/composerDraftStore';
import { useMessageQueueStore } from '@/store/messageQueueStore';

// The server refused a queued follow-up (its card is gone): say so and put the
// text back in that chat's composer, ahead of anything typed since.
export function restoreRejectedQueuedMessage(
  chatId: string,
  content: string,
  files: File[] | undefined,
  error: unknown,
): void {
  const reason = error instanceof Error && error.message ? error.message : 'unknown error';
  toast.error(`Couldn't queue your message (${reason}). It's back in the composer.`);
  const drafts = useComposerDraftStore.getState();
  const current = drafts.textByChat[chatId]?.text ?? '';
  drafts.setText(chatId, current.trim() ? `${content}\n\n${current}` : content);
  if (files?.length && !drafts.filesByChat[chatId]?.length) drafts.setFiles(chatId, files);
}

export function retryQueuedMessage(chatId: string, messageId: string): void {
  const store = useMessageQueueStore.getState();
  const message = store.queues.get(chatId)?.find((m) => m.id === messageId);
  if (!message) return;
  void store
    .retryMessage(chatId, messageId)
    .catch((error) => restoreRejectedQueuedMessage(chatId, message.content, message.files, error));
}

// The connection is back: resend every follow-up that failed on the network.
export function retryFailedQueuedMessages(): void {
  for (const [chatId, queue] of useMessageQueueStore.getState().queues) {
    for (const message of queue) {
      if (message.failed) retryQueuedMessage(chatId, message.id);
    }
  }
}
