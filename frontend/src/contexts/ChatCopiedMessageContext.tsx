import { type ReactNode, useMemo } from 'react';
import { ChatCopiedMessageContext } from './ChatCopiedMessageContextDefinition';

interface ChatCopiedMessageProviderProps {
  copiedMessageId: string | null;
  onCopy: (content: string, id: string) => void;
  children: ReactNode;
}

export function ChatCopiedMessageProvider({
  copiedMessageId,
  onCopy,
  children,
}: ChatCopiedMessageProviderProps) {
  // Kept out of ChatSessionState so per-message action bars don't re-render
  // on every stream flush — the session state object changes identity ~20x/sec
  // while streaming, but copiedMessageId only changes on copy clicks.
  const value = useMemo(() => ({ copiedMessageId, onCopy }), [copiedMessageId, onCopy]);
  return (
    <ChatCopiedMessageContext.Provider value={value}>{children}</ChatCopiedMessageContext.Provider>
  );
}
