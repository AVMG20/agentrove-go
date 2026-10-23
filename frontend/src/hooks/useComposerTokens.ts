import { useCallback, type RefObject } from 'react';
import { getHighlightTokenRanges, insertToken } from '@/utils/mentionParser';

export function useInsertToken(
  messageRef: RefObject<string>,
  setMessage: (value: string) => void,
  setCursorPosition: (pos: number) => void,
  textareaRef: RefObject<HTMLTextAreaElement | null>,
) {
  return useCallback(
    (tokenText: string, startPos: number, endPos: number) => {
      const { text, cursor } = insertToken(messageRef.current, tokenText, startPos, endPos);

      setMessage(text);

      setTimeout(() => {
        const textarea = textareaRef.current;
        if (textarea) {
          textarea.focus();
          textarea.setSelectionRange(cursor, cursor);
          setCursorPosition(cursor);
        }
      }, 0);
    },
    [messageRef, setCursorPosition, setMessage, textareaRef],
  );
}

export function useTokenDeleteKeyDown(
  messageRef: RefObject<string>,
  textareaRef: RefObject<HTMLTextAreaElement | null>,
) {
  return useCallback(
    (event: React.KeyboardEvent<Element>) => {
      // First Backspace/Delete at a pill's boundary selects token + separator space;
      // the next keystroke deletes it natively (undo intact). Backspace triggers only
      // after the separator — at the bare token end the user is still composing.
      if (event.key !== 'Backspace' && event.key !== 'Delete') return false;
      const textarea = textareaRef.current;
      if (!textarea) return false;
      const { selectionStart, selectionEnd } = textarea;
      if (selectionStart !== selectionEnd) return false;
      const message = messageRef.current;
      for (const [start, end] of getHighlightTokenRanges(message)) {
        const hasSeparator = message[end] === ' ';
        const atEdge =
          event.key === 'Backspace'
            ? hasSeparator && selectionStart === end + 1
            : selectionStart === start;
        if (atEdge) {
          event.preventDefault();
          textarea.setSelectionRange(start, hasSeparator ? end + 1 : end);
          return true;
        }
      }
      return false;
    },
    [messageRef, textareaRef],
  );
}
