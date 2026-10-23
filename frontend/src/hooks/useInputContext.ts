import {
  InputContext,
  InputStateContext,
  InputActionsContext,
} from '@/components/chat/message-input/InputContext';
import { createContextHook } from '@/hooks/createContextHook';

export const useInputContext = createContextHook(InputContext, 'useInputContext', 'InputProvider');

export const useInputState = createContextHook(InputStateContext, 'useInputState', 'InputProvider');

export const useInputActions = createContextHook(
  InputActionsContext,
  'useInputActions',
  'InputProvider',
);
