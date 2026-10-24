import { create } from 'zustand';
import { createJSONStorage, persist, type StateStorage } from 'zustand/middleware';
import { safeGetItem, safeSetItem } from '@/utils/storage';

// Unsent composer text per chat (PENDING_NEW_CHAT_KEY for the landing page),
// so switching chats or reloading doesn't wipe a half-written message. Text
// persists to localStorage within these caps; attached files stay in memory.
export const MAX_PERSISTED_DRAFTS = 50;
export const MAX_PERSISTED_DRAFT_CHARS = 20_000;
const PERSIST_DEBOUNCE_MS = 400;

interface TextDraft {
  text: string;
  updatedAt: number;
}

interface ComposerDraftState {
  textByChat: Record<string, TextDraft>;
  filesByChat: Record<string, File[]>;
  setText: (chatId: string, text: string) => void;
  setFiles: (chatId: string, files: File[]) => void;
  clearDraft: (chatId: string) => void;
}

const EMPTY_FILES: File[] = [];

export const selectDraftText = (chatId: string) => (state: ComposerDraftState) =>
  state.textByChat[chatId]?.text ?? '';

export const selectDraftFiles = (chatId: string) => (state: ComposerDraftState) =>
  state.filesByChat[chatId] ?? EMPTY_FILES;

// Newest drafts that fit the caps; an oversized draft lives in memory only.
export function pruneDraftsForStorage(
  textByChat: Record<string, TextDraft>,
): Record<string, TextDraft> {
  const kept = Object.entries(textByChat)
    .filter(([, draft]) => draft.text.length <= MAX_PERSISTED_DRAFT_CHARS)
    .sort(([, a], [, b]) => b.updatedAt - a.updatedAt)
    .slice(0, MAX_PERSISTED_DRAFTS);
  return Object.fromEntries(kept);
}

// Typing sets state per keystroke; write storage once the user pauses, and
// flush on pagehide so a reload or tab close keeps the last characters.
const pendingWrites = new Map<string, string>();
let flushTimer: ReturnType<typeof setTimeout> | null = null;

function flushPendingWrites() {
  if (flushTimer) clearTimeout(flushTimer);
  flushTimer = null;
  for (const [name, value] of pendingWrites) safeSetItem(name, value);
  pendingWrites.clear();
}

const debouncedStorage: StateStorage = {
  getItem: (name) => pendingWrites.get(name) ?? safeGetItem(name),
  setItem: (name, value) => {
    pendingWrites.set(name, value);
    if (flushTimer) clearTimeout(flushTimer);
    flushTimer = setTimeout(flushPendingWrites, PERSIST_DEBOUNCE_MS);
  },
  removeItem: (name) => {
    pendingWrites.delete(name);
    try {
      localStorage.removeItem(name);
    } catch {
      // Storage unavailable; nothing persisted to remove.
    }
  },
};

if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', flushPendingWrites);
}

function withoutKey<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record;
  const next = { ...record };
  delete next[key];
  return next;
}

export const useComposerDraftStore = create<ComposerDraftState>()(
  persist(
    (set) => ({
      textByChat: {},
      filesByChat: {},
      setText: (chatId, text) =>
        set((state) => {
          if ((state.textByChat[chatId]?.text ?? '') === text) return state;
          return {
            textByChat: text
              ? { ...state.textByChat, [chatId]: { text, updatedAt: Date.now() } }
              : withoutKey(state.textByChat, chatId),
          };
        }),
      setFiles: (chatId, files) =>
        set((state) => {
          if ((state.filesByChat[chatId] ?? EMPTY_FILES) === files) return state;
          return {
            filesByChat:
              files.length > 0
                ? { ...state.filesByChat, [chatId]: files }
                : withoutKey(state.filesByChat, chatId),
          };
        }),
      clearDraft: (chatId) =>
        set((state) => ({
          textByChat: withoutKey(state.textByChat, chatId),
          filesByChat: withoutKey(state.filesByChat, chatId),
        })),
    }),
    {
      name: 'composer-drafts',
      storage: createJSONStorage(() => debouncedStorage),
      partialize: (state) => ({ textByChat: pruneDraftsForStorage(state.textByChat) }),
    },
  ),
);
