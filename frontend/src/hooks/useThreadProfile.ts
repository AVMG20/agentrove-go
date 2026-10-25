import { useCallback, useRef } from 'react';
import { useNavigate } from 'react-router-dom';
import toast from 'react-hot-toast';
import { useUpdateChatMutation } from '@/hooks/queries/useChatQueries';
import { useProfileStore } from '@/store/profileStore';
import { resolveProfilePick } from '@/utils/profiles';
import type { AgentKind, Chat } from '@/types/chat.types';

interface UseThreadProfileOptions {
  chat: Chat | undefined;
  // The thread has messages (or a turn running): its profile is fixed.
  hasMessages: boolean;
  draft: string;
  setDraft: (value: string) => void;
}

export interface ThreadProfile {
  profileId: string | undefined;
  agentKind: AgentKind;
  profileLocked: boolean;
  onProfileChange: (profileId: string) => void;
}

// The composer's profile picker for a thread. An empty thread switches in
// place; one with messages keeps its profile and the pick opens a new thread
// in the same project on the picked profile, carrying the draft along.
export function useThreadProfile({
  chat,
  hasMessages,
  draft,
  setDraft,
}: UseThreadProfileOptions): ThreadProfile {
  const navigate = useNavigate();
  const { mutate: updateChat } = useUpdateChatMutation();
  // Read at pick time, so typing doesn't rebuild the callback.
  const draftRef = useRef(draft);
  draftRef.current = draft;

  const chatId = chat?.id;
  const currentProfileId = chat?.profile_id ?? null;
  const workspaceId = chat?.workspace_id;

  const onProfileChange = useCallback(
    (profileId: string) => {
      if (!chatId) return;
      const pick = resolveProfilePick({
        currentProfileId,
        pickedProfileId: profileId,
        hasMessages,
      });
      if (pick === 'none') return;
      useProfileStore.getState().setLastProfileId(profileId);
      if (pick === 'switch') {
        updateChat(
          { chatId, updateData: { profile_id: profileId } },
          { onError: (error) => toast.error(error.message || 'Failed to switch account') },
        );
        return;
      }
      const text = draftRef.current;
      if (text.trim()) setDraft('');
      navigate('/', {
        state: { workspaceId, initialMessage: text.trim() ? text : undefined },
      });
    },
    [chatId, currentProfileId, hasMessages, workspaceId, updateChat, setDraft, navigate],
  );

  return {
    profileId: chat?.profile_id,
    agentKind: chat?.provider ?? 'claude',
    profileLocked: hasMessages,
    onProfileChange,
  };
}
