import { useCallback, useRef, useState } from 'react';
import { logger } from '@/utils/logger';
import { extractAssistantText } from '@/utils/stream';

interface UseClipboardParams {
  chatId: string | undefined;
}

interface UseClipboardResult {
  copiedMessageId: string | null;
  handleCopy: (content: string, id: string) => Promise<void>;
}

export function useClipboard({ chatId }: UseClipboardParams): UseClipboardResult {
  const [copiedMessageId, setCopiedMessageId] = useState<string | null>(null);
  const prevChatIdRef = useRef(chatId);

  if (prevChatIdRef.current !== chatId) {
    prevChatIdRef.current = chatId;
    setCopiedMessageId(null);
  }

  const handleCopy = useCallback(async (content: string, id: string) => {
    try {
      const textToCopy = extractAssistantText(content) || content;
      await navigator.clipboard.writeText(textToCopy);
      setCopiedMessageId(id);
      setTimeout(() => setCopiedMessageId(null), 2000);
    } catch (error) {
      logger.error('Clipboard copy failed', 'useClipboard', error);
    }
  }, []);

  return {
    copiedMessageId,
    handleCopy,
  };
}
