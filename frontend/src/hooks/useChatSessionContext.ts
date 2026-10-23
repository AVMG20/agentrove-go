import {
  ChatSessionContext,
  ChatSessionStateContext,
  ChatSessionActionsContext,
} from '@/contexts/ChatSessionContextDefinition';
import { createContextHook } from '@/hooks/createContextHook';

export const useChatSessionContext = createContextHook(
  ChatSessionContext,
  'useChatSessionContext',
  'ChatSessionProvider',
);

export const useChatSessionState = createContextHook(
  ChatSessionStateContext,
  'useChatSessionState',
  'ChatSessionProvider',
);

export const useChatSessionActions = createContextHook(
  ChatSessionActionsContext,
  'useChatSessionActions',
  'ChatSessionProvider',
);
