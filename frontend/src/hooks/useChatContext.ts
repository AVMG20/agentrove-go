import { ChatContext } from '@/contexts/ChatContextDefinition';
import { createContextHook } from '@/hooks/createContextHook';

export const useChatContext = createContextHook(ChatContext, 'useChatContext', 'ChatProvider');
