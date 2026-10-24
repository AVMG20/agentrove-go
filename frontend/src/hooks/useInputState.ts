import { useCallback, type Dispatch, type SetStateAction } from 'react';
import {
  selectDraftFiles,
  selectDraftText,
  useComposerDraftStore,
} from '@/store/composerDraftStore';

interface UseInputStateParams {
  chatId: string | undefined;
}

interface UseInputStateResult {
  inputMessage: string;
  setInputMessage: Dispatch<SetStateAction<string>>;
  inputFiles: File[];
  setInputFiles: Dispatch<SetStateAction<File[]>>;
  clearInput: () => void;
}

// Composer-less panes (no chat yet) share one throwaway slot.
const NO_CHAT_KEY = '__no_chat__';

// The composer draft lives in the per-chat draft store, so it survives chat
// switches (the session remounts per chat) and, for text, reloads.
export function useInputState({ chatId }: UseInputStateParams): UseInputStateResult {
  const key = chatId ?? NO_CHAT_KEY;
  const inputMessage = useComposerDraftStore(selectDraftText(key));
  const inputFiles = useComposerDraftStore(selectDraftFiles(key));

  const setInputMessage = useCallback<Dispatch<SetStateAction<string>>>(
    (value) => {
      const store = useComposerDraftStore.getState();
      const next = typeof value === 'function' ? value(selectDraftText(key)(store)) : value;
      store.setText(key, next);
    },
    [key],
  );

  const setInputFiles = useCallback<Dispatch<SetStateAction<File[]>>>(
    (value) => {
      const store = useComposerDraftStore.getState();
      const next = typeof value === 'function' ? value(selectDraftFiles(key)(store)) : value;
      store.setFiles(key, next);
    },
    [key],
  );

  const clearInput = useCallback(() => {
    useComposerDraftStore.getState().clearDraft(key);
  }, [key]);

  return {
    inputMessage,
    setInputMessage,
    inputFiles,
    setInputFiles,
    clearInput,
  };
}
