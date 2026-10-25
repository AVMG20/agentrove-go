import { createContext } from 'react';
import type {
  AgentKind,
  ElicitationContent,
  ElicitationRequest,
  Message,
  PermissionRequest,
} from '@/types/chat.types';
import type { ContextUsageInfo } from '@/utils/contextUsage';

export interface ChatSessionState {
  messages: Message[];
  isLoading: boolean;
  isStreaming: boolean;
  isInitialLoading: boolean;
  pendingUserMessageId: string | null;
  attachedFiles: File[] | null;
  selectedModelId: string;
  // The thread's provider profile and its provider.
  selectedProfileId: string | undefined;
  agentKind: AgentKind;
  profileLocked: boolean;
  contextUsage?: ContextUsageInfo;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  pendingPermissionRequest: PermissionRequest | null;
  isPermissionLoading: boolean;
  permissionError: string | null;
  pendingElicitationRequest: ElicitationRequest | null;
  isElicitationLoading: boolean;
  elicitationError: string | null;
}

export interface ChatSessionActions {
  onSubmit: (e: React.FormEvent) => void;
  onStopStream: () => void;
  // Sends a prompt as a new user turn without touching the composer (Retry/Continue).
  onSendPrompt: (prompt: string) => void;
  onAttach: (files: File[]) => void;
  onModelChange: (modelId: string) => void;
  onProfileChange: (profileId: string) => void;
  fetchNextPage: () => void;
  onPermissionApprove: (optionId: string) => void;
  onPermissionReject: (optionId: string) => void;
  onElicitationSubmit: (content: ElicitationContent) => void;
  onElicitationSkip: () => void;
  onElicitationCancel: () => void;
}

interface ChatSessionContextValue {
  state: ChatSessionState;
  actions: ChatSessionActions;
}

export const ChatSessionContext = createContext<ChatSessionContextValue | null>(null);
export const ChatSessionStateContext = createContext<ChatSessionState | null>(null);
export const ChatSessionActionsContext = createContext<ChatSessionActions | null>(null);
