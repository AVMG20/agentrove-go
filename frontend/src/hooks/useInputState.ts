import { useCallback, useRef, useState, type Dispatch, type SetStateAction } from 'react';

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

export function useInputState({ chatId }: UseInputStateParams): UseInputStateResult {
  const [inputMessage, setInputMessage] = useState('');
  const [inputFiles, setInputFiles] = useState<File[]>([]);
  const prevChatIdRef = useRef(chatId);

  if (prevChatIdRef.current !== chatId) {
    prevChatIdRef.current = chatId;
    setInputMessage('');
    setInputFiles([]);
  }

  const clearInput = useCallback(() => {
    setInputMessage('');
    setInputFiles([]);
  }, []);

  return {
    inputMessage,
    setInputMessage,
    inputFiles,
    setInputFiles,
    clearInput,
  };
}
