import React from 'react';
import toast from 'react-hot-toast';
import type { NavigateFunction } from 'react-router-dom';
import type { QueryClient } from '@tanstack/react-query';
import {
  MessagesSquare,
  MessageSquarePlus,
  CodeXml,
  Terminal,
  GitBranch,
  Globe,
  PanelLeftClose,
  PanelRight,
  GitPullRequest,
  GitCommitHorizontal,
  ArrowUpFromLine,
  ArrowDownFromLine,
  Palette,
  Settings,
} from 'lucide-react';
import { useUIStore } from '@/store/uiStore';
import { settingsPath } from '@/components/settings/settingsSections';
import { useChatStore } from '@/store/chatStore';
import { sandboxService } from '@/services/sandboxService';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { invalidateGitState } from '@/hooks/queries/useSandboxQueries';
import { activeSplitSlot } from '@/utils/tileHelpers';
import { traverseFileStructure, getFileName } from '@/utils/file';
import { IS_MAC_PLATFORM } from '@/utils/platform';
import type { ViewType } from '@/types/ui.types';
import type { Chat } from '@/types/chat.types';
import type { FileStructure } from '@/types/file-system.types';
import type { GitBranchesData } from '@/types/sandbox.types';

export interface ViewCommandItem {
  type: 'view';
  id: ViewType;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  // Mod+Shift+<key>; see SHORTCUT_KEYS for the allowed set.
  shortcut?: string;
  hideOnMobile?: boolean;
  requiresChat?: boolean;
  requiresSandbox?: boolean;
}

export interface ActionCommandItem {
  type: 'action';
  id: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  // No shortcut = palette only; there are fewer safe chords than commands.
  shortcut?: string;
  hideOnMobile?: boolean;
  requiresChat?: boolean;
  requiresSandbox?: boolean;
}

export type CommandItem = ViewCommandItem | ActionCommandItem;

// Main-mode filter tabs (⌘[/⌘] cycle). Messages/Grep are content search, not Chats/Files.
export type MainFilter = 'all' | 'chats' | 'messages' | 'files' | 'grep' | 'actions';
export const MAIN_FILTERS: MainFilter[] = ['all', 'chats', 'messages', 'files', 'grep', 'actions'];

// Sub-modes replace the whole surface; main filters narrow the list or swap a panel.
export type MenuMode = MainFilter | 'branches' | 'themes';

export interface FlatFileItem {
  path: string;
  name: string;
}

// Title fuzzy-match rows; message-content search is the Messages tab.
export interface ChatRowItem {
  id: string;
  title: string;
  workspaceName?: string;
}

// One list for keyboard nav and sectioned rendering so order can't diverge.
export type MenuListItem =
  | { kind: 'chat'; chat: ChatRowItem }
  | { kind: 'file'; file: FlatFileItem }
  | { kind: 'command'; command: CommandItem };

// Every global chord is Mod+Shift+<key> (⌘⇧ on macOS, Ctrl+Shift elsewhere). A page
// can't intercept some of these and others open browser tools, so keys in
// RESERVED_SHORTCUT_KEYS are never bound (checked by commandRegistry.test.ts):
// - handled by the browser before the page sees them: N (incognito/private window,
//   Firefox reopen window), T (reopen tab), W (close window), P (Firefox private
//   window), Q (quit; macOS log out)
// - DevTools in Chrome/Firefox: I, J, C, K (console), E (network), M (responsive
//   design; Chrome profile switcher); R hard reload; G find previous
// - text editing in inputs: V (paste as plain text), Z (redo)
// - U (Linux IBus/GTK Unicode entry), / (macOS Help search), [ ] (macOS tab switch)
// What's left (A B D F H L O S X Y . ,) only collides with minor, overridable
// browser UI (bookmarks bar/manager, bookmark all tabs, tab search, history and
// Safari sidebar, Firefox screenshots): keydown reaches the page first and our
// preventDefault wins, and each has a menu entry. Not every command gets a chord.
export const RESERVED_SHORTCUT_KEYS = new Set([
  'n',
  't',
  'w',
  'p',
  'q',
  'i',
  'j',
  'c',
  'k',
  'e',
  'm',
  'r',
  'g',
  'v',
  'z',
  'u',
  '/',
  '[',
  ']',
]);

// Opens/closes the command menu (sidebar "Search").
export const COMMAND_MENU_SHORTCUT = 's';

const VIEW_COMMANDS: ViewCommandItem[] = [
  { type: 'view', id: 'agent', label: 'Agent', icon: MessagesSquare, shortcut: 'a' },
  { type: 'view', id: 'editor', label: 'Editor', icon: CodeXml, shortcut: 'f' },
  { type: 'view', id: 'terminal', label: 'Terminal', icon: Terminal, shortcut: 'x' },
  { type: 'view', id: 'diff', label: 'Diff', icon: GitBranch, shortcut: 'd' },
  { type: 'view', id: 'browser', label: 'Browser', icon: Globe, shortcut: 'b' },
];

const ACTION_COMMANDS: ActionCommandItem[] = [
  { type: 'action', id: 'new-thread', label: 'New thread', icon: MessageSquarePlus, shortcut: 'o' },
  {
    type: 'action',
    id: 'create-commit',
    label: 'Create commit',
    icon: GitCommitHorizontal,
    requiresChat: true,
  },
  {
    type: 'action',
    id: 'create-pr',
    label: 'Create pull request',
    icon: GitPullRequest,
    shortcut: 'l',
    requiresChat: true,
  },
  {
    type: 'action',
    id: 'create-branch',
    label: 'Create branch',
    icon: GitBranch,
    shortcut: 'h',
    requiresChat: true,
  },
  {
    type: 'action',
    id: 'switch-branch',
    label: 'Switch branch',
    icon: GitBranch,
    requiresSandbox: true,
  },
  {
    type: 'action',
    id: 'push-remote',
    label: 'Push to remote',
    icon: ArrowUpFromLine,
    requiresSandbox: true,
  },
  {
    type: 'action',
    id: 'pull-remote',
    label: 'Pull from remote',
    icon: ArrowDownFromLine,
    requiresSandbox: true,
  },
];

export const SESSION_PANEL_SHORTCUT = 'y';

const SETTING_COMMANDS: ActionCommandItem[] = [
  {
    type: 'action',
    id: 'toggle-sidebar',
    label: 'Toggle sidebar',
    icon: PanelLeftClose,
    shortcut: '.',
  },
  {
    type: 'action',
    id: 'toggle-session-panel',
    label: 'Toggle session panel',
    icon: PanelRight,
    shortcut: SESSION_PANEL_SHORTCUT,
    hideOnMobile: true,
    requiresChat: true,
  },
  // Opens the theme picker sub-mode (every theme as a list) instead of one command per
  // theme — keeps the chord space sane as themes grow.
  { type: 'action', id: 'set-theme', label: 'Set theme', icon: Palette },
  { type: 'action', id: 'open-settings', label: 'Settings', icon: Settings, shortcut: ',' },
];

export const ALL_COMMANDS: CommandItem[] = [
  ...ACTION_COMMANDS,
  ...SETTING_COMMANDS,
  ...VIEW_COMMANDS,
];

const PUNCTUATION_CODES: Record<string, string> = { '.': 'Period', ',': 'Comma' };

const shortcutToCode = (key: string) => PUNCTUATION_CODES[key] ?? `Key${key.toUpperCase()}`;

export const COMMAND_MENU_SHORTCUT_CODE = shortcutToCode(COMMAND_MENU_SHORTCUT);

export const SHORTCUT_MAP = new Map<string, CommandItem>(
  ALL_COMMANDS.flatMap((cmd) => (cmd.shortcut ? [[shortcutToCode(cmd.shortcut), cmd]] : [])),
);

// Hint text for a command's chord, or undefined when it has none.
export function commandShortcutLabel(id: string): string | undefined {
  const shortcut = ALL_COMMANDS.find((cmd) => cmd.id === id)?.shortcut;
  return shortcut ? formatShortcut(shortcut) : undefined;
}

// Commands that switch the menu into a sub-mode instead of dispatching an action.
export const COMMAND_TO_MODE: Partial<Record<string, MenuMode>> = {
  'switch-branch': 'branches',
  'set-theme': 'themes',
};

export const flattenFiles = (files: FileStructure[]): FlatFileItem[] =>
  traverseFileStructure(files, (item) =>
    item.type === 'file' ? { path: item.path, name: getFileName(item.path) } : null,
  );

export function formatShortcut(key: string): string {
  const mod = IS_MAC_PLATFORM ? '⌘' : 'Ctrl';
  return `${mod}⇧${key === '.' ? '.' : key.toUpperCase()}`;
}

// The sandbox a git action targets. Resolved from the active pane's chat context
// (or the selected workspace on the landing page), decoupled from whether a chat exists.
export interface GitTarget {
  sandboxId?: string;
  worktreeCwd?: string;
}

// Chat-scoped actions (scripts, git) target the pane the user last interacted with —
// in split view a split pane is a different chat. The split chat is cache-warm
// here since it's rendered in the split.
function getActiveChat(queryClient: QueryClient): Chat | null {
  const ui = useUIStore.getState();
  const slot = activeSplitSlot(ui.activeAgentTile, ui.splitChatIds);
  const splitChatId = slot ? ui.splitChatIds[slot - 1] : undefined;
  if (splitChatId) {
    // Don't fall back to the primary chat — acting on the wrong chat is worse than
    // a no-op if the split chat isn't cached yet.
    return queryClient.getQueryData<Chat>(queryKeys.chat(splitChatId)) ?? null;
  }
  return useChatStore.getState().currentChat;
}

// Git target for the global keyboard-shortcut path, which has no chat context.
// Prefers the active chat; on the landing page (no chat) it falls back to the
// selected workspace's sandbox so branch/pull/push shortcuts still resolve.
export function resolveActiveGitTarget(queryClient: QueryClient): GitTarget {
  const chat = getActiveChat(queryClient);
  if (chat?.sandbox_id) {
    return { sandboxId: chat.sandbox_id, worktreeCwd: chat.worktree_cwd ?? undefined };
  }
  return { sandboxId: useUIStore.getState().workspaceSandboxId ?? undefined };
}

function executeGitRemoteCommand(
  target: GitTarget,
  fn: (
    sandboxId: string,
    cwd?: string,
  ) => Promise<{ success: boolean; output: string; error?: string }>,
  label: string,
  onSuccess?: () => void,
) {
  if (!target.sandboxId) {
    toast.error('No project selected');
    return;
  }
  void fn(target.sandboxId, target.worktreeCwd)
    .then((r) => {
      if (r.success) {
        toast.success(`${label}${r.output ? `: ${r.output.slice(0, 80)}` : ''}`);
        onSuccess?.();
      } else {
        toast.error(r.error || `${label} failed`);
      }
    })
    .catch(() => toast.error(`${label} failed`));
}

export function executeCommand(
  cmd: CommandItem,
  queryClient: QueryClient,
  navigate: NavigateFunction,
  toggle: boolean,
  gitTarget: GitTarget,
) {
  const ui = useUIStore.getState();

  if (cmd.type === 'view') {
    // Toggling is scoped to the active pane (the chat the user last interacted with).
    ui.toggleView(cmd.id, toggle);
  } else if (cmd.id === 'new-thread') {
    navigate('/');
  } else if (cmd.id === 'create-commit') {
    ui.setCreateCommitDialogOpen(toggle ? !ui.createCommitDialogOpen : true);
  } else if (cmd.id === 'create-pr') {
    ui.setCreatePRDialogOpen(toggle ? !ui.createPRDialogOpen : true);
  } else if (cmd.id === 'create-branch') {
    ui.setCreateBranchDialogOpen(toggle ? !ui.createBranchDialogOpen : true);
  } else if (cmd.id === 'push-remote') {
    const sandboxId = gitTarget.sandboxId;
    executeGitRemoteCommand(gitTarget, sandboxService.gitPush, 'Pushed to remote', () => {
      if (sandboxId) {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.sandbox.gitBranchesAll(sandboxId),
        });
      }
    });
  } else if (cmd.id === 'pull-remote') {
    const sandboxId = gitTarget.sandboxId;
    executeGitRemoteCommand(gitTarget, sandboxService.gitPull, 'Pulled from remote', () => {
      if (sandboxId) {
        void Promise.all([
          queryClient.invalidateQueries({
            queryKey: queryKeys.sandbox.gitBranchesAll(sandboxId),
          }),
          queryClient.invalidateQueries({
            queryKey: queryKeys.sandbox.filesMetadataAll(sandboxId),
          }),
          // The pull rewrote files: open tabs must not keep (and save over) old content.
          queryClient.invalidateQueries({
            queryKey: queryKeys.sandbox.fileContentAll(sandboxId),
          }),
          invalidateGitState(queryClient, sandboxId),
        ]);
      }
    });
  } else if (cmd.id === 'toggle-sidebar') {
    ui.setSidebarOpen(!ui.sidebarOpen);
  } else if (cmd.id === 'toggle-session-panel') {
    ui.setSessionPanelOpen(!ui.sessionPanelOpen);
  } else if (cmd.id === 'set-theme') {
    ui.setPendingMenuMode('themes');
    ui.setCommandMenuOpen(true);
  } else if (cmd.id === 'open-settings') {
    navigate(settingsPath());
  } else if (cmd.id === 'switch-branch') {
    if (!gitTarget.sandboxId) {
      toast.error('No project selected');
      return;
    }
    // Bail out only if we know the repo state and it's unavailable; if the cache is cold,
    // let the menu open and fall through to its loading/empty states.
    const cached = queryClient.getQueryData<GitBranchesData>(
      queryKeys.sandbox.gitBranches(gitTarget.sandboxId, gitTarget.worktreeCwd),
    );
    if (cached && (!cached.is_git_repo || cached.branches.length === 0)) {
      toast.error('No git branches available');
      return;
    }
    ui.setPendingMenuMode('branches');
    ui.setCommandMenuOpen(true);
  }
}
