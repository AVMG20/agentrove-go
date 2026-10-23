import { create } from 'zustand';
import { persist } from 'zustand/middleware';

type PermissionMode = 'default' | 'acceptEdits' | 'auto' | 'plan' | 'bypassPermissions';

const DEFAULT_KEY = '__default__';
export const DEFAULT_PERMISSION_MODE: PermissionMode = 'bypassPermissions';
export const DEFAULT_THINKING_MODE: string = 'high';
export const DEFAULT_WORKTREE = false;
export const DEFAULT_PERSONA = 'Default';

interface ChatSettingsState {
  permissionModeByChat: Record<string, PermissionMode>;
  thinkingModeByChat: Record<string, string>;
  worktreeByChat: Record<string, boolean>;
  // Base branch a new worktree is cut from; undefined = follow the current branch.
  worktreeBaseBranchByChat: Record<string, string | undefined>;
  personaByChat: Record<string, string>;
  setPermissionMode: (chatId: string, mode: PermissionMode) => void;
  setThinkingMode: (chatId: string, mode: string) => void;
  setWorktree: (chatId: string, enabled: boolean) => void;
  setWorktreeBaseBranch: (chatId: string, branch: string | undefined) => void;
  setPersona: (chatId: string, name: string) => void;
  initChatFromDefaults: (chatId: string) => void;
}

export const useChatSettingsStore = create<ChatSettingsState>()(
  persist(
    (set, get) => ({
      permissionModeByChat: {},
      thinkingModeByChat: {},
      worktreeByChat: {},
      worktreeBaseBranchByChat: {},
      personaByChat: {},
      setPermissionMode: (chatId, mode) =>
        set((state) => ({
          permissionModeByChat: {
            ...state.permissionModeByChat,
            [chatId]: mode,
          },
        })),
      setThinkingMode: (chatId, mode) =>
        set((state) => ({
          thinkingModeByChat: { ...state.thinkingModeByChat, [chatId]: mode },
        })),
      setWorktree: (chatId, enabled) =>
        set((state) => ({
          worktreeByChat: { ...state.worktreeByChat, [chatId]: enabled },
        })),
      setWorktreeBaseBranch: (chatId, branch) =>
        set((state) => {
          // Delete rather than store `undefined` so persist/rehydrate round-trips to
          // the same shape — one representation of "no base" (absent key).
          const next = { ...state.worktreeBaseBranchByChat };
          if (branch === undefined) {
            delete next[chatId];
          } else {
            next[chatId] = branch;
          }
          return { worktreeBaseBranchByChat: next };
        }),
      setPersona: (chatId, name) =>
        set((state) => ({
          personaByChat: { ...state.personaByChat, [chatId]: name },
        })),
      // Seed a new chat from __default__ toolbar settings.
      initChatFromDefaults: (chatId) => {
        const state = get();
        const permission = state.permissionModeByChat[DEFAULT_KEY];
        const thinking = state.thinkingModeByChat[DEFAULT_KEY];
        const worktree = state.worktreeByChat[DEFAULT_KEY];
        const baseBranch = state.worktreeBaseBranchByChat[DEFAULT_KEY];
        const persona = state.personaByChat[DEFAULT_KEY];
        const updates: Partial<
          Pick<
            ChatSettingsState,
            | 'permissionModeByChat'
            | 'thinkingModeByChat'
            | 'worktreeByChat'
            | 'worktreeBaseBranchByChat'
            | 'personaByChat'
          >
        > = {};
        if (permission !== undefined) {
          updates.permissionModeByChat = {
            ...state.permissionModeByChat,
            [chatId]: permission,
          };
        }
        if (thinking !== undefined) {
          updates.thinkingModeByChat = { ...state.thinkingModeByChat, [chatId]: thinking };
        }
        if (worktree !== undefined) {
          updates.worktreeByChat = { ...state.worktreeByChat, [chatId]: worktree };
        }
        if (baseBranch !== undefined) {
          updates.worktreeBaseBranchByChat = {
            ...state.worktreeBaseBranchByChat,
            [chatId]: baseBranch,
          };
        }
        if (persona !== undefined) {
          updates.personaByChat = { ...state.personaByChat, [chatId]: persona };
        }
        if (Object.keys(updates).length > 0) set(updates);
      },
    }),
    { name: 'chat-settings-storage' },
  ),
);

export { DEFAULT_KEY as DEFAULT_CHAT_SETTINGS_KEY };
export type { PermissionMode };
