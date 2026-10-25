import { useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import toast from 'react-hot-toast';
import { chatService } from '@/services/chatService';
import { useCreateChatMutation } from '@/hooks/queries/useChatQueries';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import { useModelStore } from '@/store/modelStore';
import { useStreamStore } from '@/store/streamStore';
import { useChatSettingsStore } from '@/store/chatSettingsStore';
import { coercePermissionMode } from '@/components/chat/permission-mode-selector/permissionModes';
import { coerceThinkingMode } from '@/components/chat/thinking-mode-selector/thinkingModes';
import type { Chat } from '@/types/chat.types';
import type { StreamAction } from '@/types/user.types';

// Spawns a stream action as a new thread in the source chat's worktree (so it
// sees the changes), starts the turn server-side, then navigates there.
export function useRunStreamAction(sourceChat: Chat | undefined) {
  const navigate = useNavigate();
  const createChat = useCreateChatMutation();
  const modelMap = useModelMap();

  return useCallback(
    async (action: StreamAction) => {
      if (!sourceChat) return;

      const model = modelMap.get(action.model_id);
      const permissionMode = coercePermissionMode(action.permission_mode);
      const thinkingMode = coerceThinkingMode(action.thinking_mode, model);

      try {
        const newChat = await createChat.mutateAsync({
          title: action.label,
          model_id: action.model_id,
          workspace_id: sourceChat.workspace_id,
          worktree_chat_id: sourceChat.id,
        });

        useModelStore.getState().selectModel(newChat.id, action.model_id);
        const settings = useChatSettingsStore.getState();
        settings.setPersona(newChat.id, action.persona_name);
        settings.setPermissionMode(newChat.id, permissionMode);
        settings.setThinkingMode(newChat.id, thinkingMode);

        const { messageId } = await chatService.startCompletion({
          prompt: action.command,
          chat_id: newChat.id,
          model_id: action.model_id,
          permission_mode: permissionMode,
          thinking_mode: thinkingMode,
          selected_persona_name: action.persona_name,
        });

        // No client EventSource here — the global stream watcher reconciles this
        // metadata against the server and prunes the pulse once the turn settles.
        useStreamStore.getState().addStreamMetadata({
          chatId: newChat.id,
          messageId,
          startTime: Date.now(),
        });

        toast.success(`Started "${action.label}"`);
        // Navigation also adds the tab via loadWorkspaceForChat.
        navigate(`/chat/${newChat.id}`);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : `Failed to start "${action.label}"`);
      }
    },
    [sourceChat, createChat, navigate, modelMap],
  );
}
