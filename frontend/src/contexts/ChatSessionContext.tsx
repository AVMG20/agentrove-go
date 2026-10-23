import { type ReactNode, useMemo } from 'react';
import {
  ChatSessionContext,
  ChatSessionStateContext,
  ChatSessionActionsContext,
  type ChatSessionState,
  type ChatSessionActions,
} from './ChatSessionContextDefinition';

interface ChatSessionProviderProps {
  state: ChatSessionState;
  actions: ChatSessionActions;
  children: ReactNode;
}

export function ChatSessionProvider({ state, actions, children }: ChatSessionProviderProps) {
  const value = useMemo(() => ({ state, actions }), [state, actions]);
  return (
    <ChatSessionContext.Provider value={value}>
      <ChatSessionStateContext.Provider value={state}>
        <ChatSessionActionsContext.Provider value={actions}>
          {children}
        </ChatSessionActionsContext.Provider>
      </ChatSessionStateContext.Provider>
    </ChatSessionContext.Provider>
  );
}
