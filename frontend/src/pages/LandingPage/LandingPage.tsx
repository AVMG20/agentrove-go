import { useState, useRef, useMemo, useCallback, useEffect, ReactNode, Suspense } from 'react';
import clsx from 'clsx';
import { lazyNamed } from '@/utils/lazyNamed';
import { useMountEffect } from '@/hooks/useMountEffect';
import { useNavigate, useLocation } from 'react-router-dom';
import toast from 'react-hot-toast';
import { Sidebar } from '@/components/layout/Sidebar/Sidebar';
import { useLayoutSidebar } from '@/components/layout/Layout/layoutState';
import { Input as ChatInput } from '@/components/chat/message-input/Input';
import { WorkspaceSelector } from '@/components/chat/workspace-selector/WorkspaceSelector';
import { BranchWorktreeSelector } from '@/components/chat/worktree-selector/BranchWorktreeSelector';
import { useChatStore } from '@/store/chatStore';
import { useUIStore } from '@/store/uiStore';
import { useModelStore } from '@/store/modelStore';
import { useChatSettingsStore, DEFAULT_CHAT_SETTINGS_KEY } from '@/store/chatSettingsStore';
import { useAuthStore } from '@/store/authStore';
import { useCreateChatMutation } from '@/hooks/queries/useChatQueries';
import { useWorkspacesList, useWorkspaceResourcesQuery } from '@/hooks/queries/useWorkspaceQueries';
import { useFilesMetadataQuery } from '@/hooks/queries/useSandboxQueries';
import { useModelSelection } from '@/hooks/queries/useModelQueries';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import { ChatProvider } from '@/contexts/ChatContext';
import { Button } from '@/components/ui/primitives/Button/Button';
import { buildFileStructureFromSandboxFiles } from '@/utils/file';
import { SplitViewContainer } from '@/components/ui/SplitViewContainer/SplitViewContainer';
import { CommandMenu } from '@/components/ui/command-menu/CommandMenu';
import { useCommandMenu } from '@/hooks/useCommandMenu';
import { useEditorState } from '@/hooks/useEditorState';
import { viewLoadingFallback } from '@/components/ui/shared/ViewLoadingFallback/ViewLoadingFallback';
import { PENDING_NEW_CHAT_KEY, type TileId } from '@/types/ui.types';
import { tileIdToViewType } from '@/utils/tileHelpers';
import { useCurrentUserQuery } from '@/hooks/queries/useAuthQueries';
import { useSidebarChatLists } from '@/hooks/queries/useSidebarChatLists';
import { RecentChats } from './RecentChats';
import iconDark from '/assets/images/icon-dark.svg';
import iconLight from '/assets/images/icon-white.svg';
import styles from './LandingPage.module.scss';

const Editor = lazyNamed(() => import('@/components/editor/editor-core/Editor'), 'Editor');
const DiffView = lazyNamed(() => import('@/components/sandbox/git/DiffView/DiffView'), 'DiffView');
const TerminalContainer = lazyNamed(
  () => import('@/components/sandbox/terminal/Container/Container'),
  'Container',
);

const EXAMPLE_PROMPTS = [
  'Build a REST API with authentication',
  'Find and fix bugs in my codebase',
  'Refactor this project to use TypeScript',
];

function getTimeGreeting(): string {
  const hour = new Date().getHours();
  if (hour < 12) return 'Good morning';
  if (hour < 18) return 'Good afternoon';
  return 'Good evening';
}

// Base-branch intent is workspace-scoped, so switching workspace resets it.
const clearDefaultWorktreeBase = () =>
  useChatSettingsStore.getState().setWorktreeBaseBranch(DEFAULT_CHAT_SETTINGS_KEY, undefined);

export function LandingPage() {
  const navigate = useNavigate();
  const location = useLocation();
  useCommandMenu();
  const attachedFiles = useChatStore(
    (state) => state.attachedFilesByChat[PENDING_NEW_CHAT_KEY] ?? null,
  );
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const { selectedModelId, selectModel } = useModelSelection({
    enabled: isAuthenticated,
  });

  const workspaces = useWorkspacesList({ enabled: isAuthenticated });

  const { data: currentUser } = useCurrentUserQuery({ enabled: isAuthenticated });
  // Same list as the sidebar (empty → example prompts).
  const { recentChats, workspaceBadgeById, isLoadingChats } = useSidebarChatLists(
    workspaces,
    isAuthenticated,
  );
  const showRecents = !isLoadingChats && recentChats.length > 0;
  const greeting = getTimeGreeting();
  // Handles are lowercase — capitalize for the greeting.
  const username = currentUser?.username ?? '';
  const greetingName = username && username.charAt(0).toUpperCase() + username.slice(1);

  const createChat = useCreateChatMutation();
  const [message, setMessage] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const consumedInitialMessageRef = useRef(false);
  const routeState = location.state as {
    workspaceId?: string;
    initialMessage?: string;
  } | null;
  const initialWorkspaceId = routeState?.workspaceId ?? null;
  const initialMessage = routeState?.initialMessage ?? null;
  // location.key (not target id) so re-clicking the same "new thread" re-applies it.
  const consumedNavKeyRef = useRef<string | null>(null);

  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<string | null>(null);

  // Single entry points for selection changes: clear the workspace-scoped default
  // base only on a genuine switch (prior selection existed and differs) so
  // auto-select (null -> first) and reload keep a persisted base for the same workspace.
  const handleWorkspaceChange = useCallback(
    (id: string | null) => {
      if (selectedWorkspaceId !== null && id !== selectedWorkspaceId) clearDefaultWorktreeBase();
      setSelectedWorkspaceId(id);
    },
    [selectedWorkspaceId],
  );

  // Default local workspace when list loads or current pick is gone.
  useEffect(() => {
    if (!workspaces.length) return;
    if (selectedWorkspaceId && workspaces.some((ws) => ws.id === selectedWorkspaceId)) return;
    handleWorkspaceChange(workspaces[0].id);
  }, [workspaces, selectedWorkspaceId, handleWorkspaceChange]);

  // One-shot per navigation: preselect workspace.
  useEffect(() => {
    if (location.key === consumedNavKeyRef.current) return;
    consumedNavKeyRef.current = location.key;
    if (initialWorkspaceId) handleWorkspaceChange(initialWorkspaceId);
  }, [location.key, initialWorkspaceId, handleWorkspaceChange]);

  if (initialMessage && !consumedInitialMessageRef.current) {
    consumedInitialMessageRef.current = true;
    setMessage(initialMessage);
  }

  const { data: workspaceResources } = useWorkspaceResourcesQuery(
    selectedWorkspaceId ?? undefined,
    {
      enabled: isAuthenticated && !!selectedWorkspaceId,
    },
  );

  const { data: settings } = useSettingsQuery({ enabled: isAuthenticated });

  // Sandbox for editor/@file/branch before a chat exists.
  const selectedSandboxId = workspaces.find((ws) => ws.id === selectedWorkspaceId)?.sandbox_id;

  const {
    data: filesMetadata = [],
    isLoading: isFilesMetadataLoading,
    refetch: refetchFilesMetadata,
  } = useFilesMetadataQuery(selectedSandboxId, undefined, {
    enabled: isAuthenticated && !!selectedSandboxId,
  });

  const fileStructure = useMemo(
    () => buildFileStructureFromSandboxFiles(filesMetadata),
    [filesMetadata],
  );

  const { selectedFile, setSelectedFile, isRefreshing, handleRefresh, openFiles, closeFile } =
    useEditorState(refetchFilesMetadata, undefined, fileStructure);

  // Landing editor tabs are store-backed — clear on sandbox change (effect, not render).
  useEffect(() => {
    setSelectedFile(null);
  }, [selectedSandboxId, setSelectedFile]);

  // Global git shortcuts need a sandbox on landing; clear on leave so it doesn't leak.
  useEffect(() => {
    useUIStore.getState().setWorkspaceSandboxId(selectedSandboxId ?? null);
    return () => useUIStore.getState().setWorkspaceSandboxId(null);
  }, [selectedSandboxId]);

  useMountEffect(() => {
    useChatStore.getState().setCurrentChat(null);
    // Drop a prior chat's split layout so it doesn't stick on landing.
    useUIStore.getState().resetWorkspace();
  });

  const handleFileAttach = useCallback((files: File[]) => {
    useChatStore.getState().setAttachedFilesForChat(PENDING_NEW_CHAT_KEY, files);
  }, []);

  const handleNewChat = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      const trimmedPrompt = message.trim();
      if (!trimmedPrompt || isLoading) return;

      if (!isAuthenticated) {
        navigate('/signup');
        return;
      }

      if (!selectedModelId?.trim()) {
        toast.error('Please select an AI model');
        return;
      }

      const title = trimmedPrompt.replace(/\s+/g, ' ').slice(0, 80) || 'New Chat';

      if (!selectedWorkspaceId) {
        toast.error('Please select a workspace');
        return;
      }

      setIsLoading(true);
      try {
        const newChat = await createChat.mutateAsync({
          title,
          model_id: selectedModelId,
          workspace_id: selectedWorkspaceId,
        });
        useModelStore.getState().selectModel(newChat.id, selectedModelId);
        useChatSettingsStore.getState().initChatFromDefaults(newChat.id);
        setMessage('');
        useChatStore.getState().promoteAttachedFiles(PENDING_NEW_CHAT_KEY, newChat.id);
        navigate(`/chat/${newChat.id}`, { state: { initialPrompt: trimmedPrompt } });
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to create chat');
      } finally {
        setIsLoading(false);
      }
    },
    [
      createChat,
      isAuthenticated,
      isLoading,
      message,
      navigate,
      selectedModelId,
      selectedWorkspaceId,
    ],
  );

  const handleChatSelect = useCallback(
    (chatId: string) => {
      navigate(`/chat/${chatId}`);
    },
    [navigate],
  );

  const composerSelectors = useMemo(
    () => (
      <div className={styles['selector-row']}>
        <WorkspaceSelector
          selectedWorkspaceId={selectedWorkspaceId}
          onWorkspaceChange={handleWorkspaceChange}
          enabled={isAuthenticated}
        />
        <BranchWorktreeSelector disabled={isLoading} />
      </div>
    ),
    [selectedWorkspaceId, handleWorkspaceChange, isLoading, isAuthenticated],
  );

  const sidebarContent = useMemo(() => {
    if (!isAuthenticated) return null;

    return (
      <Sidebar workspaces={workspaces} selectedChatId={null} onChatSelect={handleChatSelect} />
    );
  }, [workspaces, handleChatSelect, isAuthenticated]);

  useLayoutSidebar(sidebarContent);

  const renderView = useCallback(
    (tileId: TileId, isVisible: boolean): ReactNode => {
      switch (tileIdToViewType(tileId)) {
        case 'agent':
          return (
            <div className={styles['agent-view']}>
              <div className={styles['agent-panel']}>
                <div className={styles.greeting}>
                  <img
                    src={iconDark}
                    alt="Agentrove"
                    className={clsx(styles['greeting-logo'], styles['greeting-logo--on-light'])}
                  />
                  <img
                    src={iconLight}
                    alt="Agentrove"
                    className={clsx(styles['greeting-logo'], styles['greeting-logo--on-dark'])}
                  />
                  <h1 className={styles['greeting-title']}>
                    {greeting}
                    {greetingName && `, ${greetingName}`}
                  </h1>
                  <p className={styles['greeting-subtitle']}>What are we building today?</p>
                </div>

                {composerSelectors}

                <ChatInput
                  message={message}
                  setMessage={setMessage}
                  onSubmit={handleNewChat}
                  onAttach={handleFileAttach}
                  attachedFiles={attachedFiles}
                  isLoading={isLoading}
                  showLoadingSpinner={true}
                  selectedModelId={selectedModelId}
                  onModelChange={selectModel}
                  showTip={false}
                  showBranch={false}
                  placeholder="Message Agentrove... (@ to mention, / for commands)"
                />

                <div className={styles['panel-below']}>
                  {showRecents ? (
                    <RecentChats
                      chats={recentChats}
                      workspaceBadgeById={workspaceBadgeById}
                      onChatSelect={handleChatSelect}
                    />
                  ) : (
                    <div className={styles['example-prompts']}>
                      {EXAMPLE_PROMPTS.map((prompt) => (
                        <Button
                          key={prompt}
                          type="button"
                          variant="unstyled"
                          onClick={() => setMessage(prompt)}
                          className={styles['example-prompt']}
                        >
                          {prompt}
                        </Button>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </div>
          );
        case 'editor':
          return (
            <Suspense fallback={viewLoadingFallback}>
              <Editor
                files={fileStructure}
                selectedFile={selectedFile}
                onFileSelect={setSelectedFile}
                openFiles={openFiles}
                onCloseFile={closeFile}
                sandboxId={selectedSandboxId}
                isSandboxSyncing={isFilesMetadataLoading}
                onRefresh={handleRefresh}
                isRefreshing={isRefreshing}
                chatId={undefined}
              />
            </Suspense>
          );
        case 'diff':
          // Pre-chat: workspace sandbox root; review comments off (no chatId).
          return (
            <Suspense fallback={viewLoadingFallback}>
              <DiffView chatId={undefined} sandboxId={selectedSandboxId} isVisible={isVisible} />
            </Suspense>
          );
        case 'terminal':
          // storageScope by sandbox (PTY keys); unmount on toggle — restore-on-mount keeps sessions.
          return (
            <Suspense fallback={viewLoadingFallback}>
              <TerminalContainer
                sandboxId={selectedSandboxId}
                storageScope={selectedSandboxId ? `landing-${selectedSandboxId}` : undefined}
                isVisible={isVisible}
                panelKey={`tile-${tileId}`}
              />
            </Suspense>
          );
        default:
          return null;
      }
    },
    [
      attachedFiles,
      handleFileAttach,
      handleNewChat,
      handleChatSelect,
      isLoading,
      message,
      selectModel,
      selectedModelId,
      composerSelectors,
      showRecents,
      recentChats,
      workspaceBadgeById,
      greeting,
      greetingName,
      fileStructure,
      selectedFile,
      setSelectedFile,
      openFiles,
      closeFile,
      selectedSandboxId,
      isFilesMetadataLoading,
      handleRefresh,
      isRefreshing,
    ],
  );

  return (
    <ChatProvider
      sandboxId={selectedSandboxId}
      fileStructure={fileStructure}
      customSkills={workspaceResources?.skills}
      builtinSlashCommands={workspaceResources?.builtin_slash_commands}
      personas={settings?.personas}
    >
      <div className={styles.surface}>
        <SplitViewContainer renderView={renderView} />
      </div>
      <CommandMenu />
    </ChatProvider>
  );
}
