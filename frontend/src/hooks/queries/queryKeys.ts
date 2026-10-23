import type { PullRequestFilters } from '@/types/github.types';
import type { DiffMode } from '@/types/sandbox.types';

export const queryKeys = {
  chats: 'chats',
  chatsSearch: (query: string) => ['chats', 'search', query] as const,
  chatsSearchAll: ['chats', 'search'] as const,
  chatsRecent: ['chats', 'recent'] as const,
  chat: (chatId?: string) => ['chat', chatId] as const,
  messages: (chatId?: string) => ['messages', chatId] as const,
  messageRender: (messageId: string) => ['message-render', messageId] as const,
  contextUsage: (chatId?: string) => ['chat', chatId, 'context-usage'] as const,
  subThreads: (chatId?: string) => ['chat', chatId, 'sub-threads'] as const,
  auth: {
    user: 'auth-user',
    config: ['auth-config'] as const,
  },
  settings: 'settings',
  skills: 'skills',
  sandbox: {
    fileContent: (sandboxId?: string, filePath?: string) =>
      ['sandbox', sandboxId, 'file-content', filePath] as const,
    fileContentAll: (sandboxId?: string) => ['sandbox', sandboxId, 'file-content'] as const,
    filesMetadata: (sandboxId?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'files-metadata', cwd] as const,
    filesMetadataAll: (sandboxId?: string) => ['sandbox', sandboxId, 'files-metadata'] as const,
    gitDiff: (
      sandboxId: string | undefined,
      mode: DiffMode,
      fullContext: boolean = false,
      cwd?: string,
    ) => ['sandbox', sandboxId, 'git-diff', mode, fullContext, cwd] as const,
    gitDiffAll: (sandboxId?: string) => ['sandbox', sandboxId, 'git-diff'] as const,
    // Under the git-diff prefix so every gitDiffAll invalidation refreshes it.
    gitDiffStats: (sandboxId?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'git-diff', 'stats', cwd] as const,
    gitFileBaseline: (sandboxId?: string, path?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'git-file-baseline', path, cwd] as const,
    gitFileBaselineAll: (sandboxId?: string) =>
      ['sandbox', sandboxId, 'git-file-baseline'] as const,
    gitChangedPaths: (sandboxId?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'git-changed-paths', cwd] as const,
    gitChangedPathsAll: (sandboxId?: string) =>
      ['sandbox', sandboxId, 'git-changed-paths'] as const,
    gitBranches: (sandboxId?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'git-branches', cwd] as const,
    gitBranchesAll: (sandboxId?: string) => ['sandbox', sandboxId, 'git-branches'] as const,
    gitRemoteUrl: (sandboxId?: string, cwd?: string) =>
      ['sandbox', sandboxId, 'git-remote-url', cwd] as const,
    search: (
      sandboxId: string | undefined,
      query: string,
      cwd: string | undefined,
      caseSensitive: boolean = false,
      regex: boolean = false,
      wholeWord: boolean = false,
      include: string = '',
      exclude: string = '',
    ) =>
      [
        'sandbox',
        sandboxId,
        'search',
        query,
        cwd,
        caseSensitive,
        regex,
        wholeWord,
        include,
        exclude,
      ] as const,
    searchAll: (sandboxId?: string) => ['sandbox', sandboxId, 'search'] as const,
  },
  workspaces: ['workspaces'] as const,
  workspaceDiskUsage: ['workspaces', 'disk-usage'] as const,
  workspaceResources: (workspaceId?: string) => ['workspaces', workspaceId, 'resources'] as const,
  automations: ['automations'] as const,
  artifacts: ['artifacts'] as const,
  // Under the artifacts prefix so list invalidations refresh open viewers too.
  artifactView: (artifactId?: string) => ['artifacts', artifactId, 'view'] as const,
  claudeUsage: ['usage', 'claude'] as const,
  channels: (workspaceId?: string) => ['channels', workspaceId ?? null] as const,
  channelsAll: ['channels'] as const,
  channel: (channelId?: string) => ['channel', channelId] as const,
  channelMessageActivity: (channelId: string, messageId: string) =>
    ['channel', channelId, 'activity', messageId] as const,
  models: 'models',
  github: {
    repos: (query: string) => ['github-repos', query] as const,
    pullsAll: (owner: string, repo: string) => ['github-pulls', owner, repo] as const,
    pulls: (owner: string, repo: string, filters?: PullRequestFilters) =>
      [...queryKeys.github.pullsAll(owner, repo), filters ?? {}] as const,
    collaborators: (owner: string, repo: string) => ['github-collaborators', owner, repo] as const,
  },
} as const;
