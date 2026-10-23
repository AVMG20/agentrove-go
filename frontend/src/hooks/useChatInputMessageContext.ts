import {
  ChatInputMessageContext,
  SetChatInputMessageContext,
} from '@/contexts/ChatInputMessageContextDefinition';
import { createContextHook } from '@/hooks/createContextHook';

export const useChatInputMessageContext = createContextHook(
  ChatInputMessageContext,
  'useChatInputMessageContext',
  'ChatInputMessageProvider',
);

export const useSetChatInputMessage = createContextHook(
  SetChatInputMessageContext,
  'useSetChatInputMessage',
  'ChatInputMessageProvider',
);
