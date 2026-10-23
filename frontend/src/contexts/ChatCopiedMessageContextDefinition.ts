import { createContext } from 'react';

interface ChatCopiedMessageContextValue {
  copiedMessageId: string | null;
  onCopy: (content: string, id: string) => void;
}

export const ChatCopiedMessageContext = createContext<ChatCopiedMessageContextValue | null>(null);
