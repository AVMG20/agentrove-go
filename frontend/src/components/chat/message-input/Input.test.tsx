// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Input, type InputProps } from './Input';

// The composer's send/queue decision lives in the real useInputSubmit hook,
// which Input wires up through InputProvider. Keep that hook real and mock only
// the surrounding data sources so the test exercises the actual dispatch logic.
const mocks = vi.hoisted(() => ({
  queueMessage: vi.fn<(...args: unknown[]) => Promise<string>>(() => Promise.resolve('queued-id')),
  clearComposerSelections: vi.fn(),
  removeComposerSelection: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock('@/hooks/useChatContext', () => ({
  useChatContext: () => ({
    fileStructure: null,
    customSkills: [],
    builtinSlashCommands: [],
    personas: [],
  }),
}));

vi.mock('@/store/authStore', () => ({
  useAuthStore: (selector: (s: { isAuthenticated: boolean }) => unknown) =>
    selector({ isAuthenticated: true }),
}));

vi.mock('@/hooks/queries/useModelQueries', () => ({
  useModelMap: () => new Map(),
}));

vi.mock('@/hooks/queries/useChatQueries', () => ({
  useRecentChatsQuery: () => ({ data: undefined }),
}));

const uiState = { composerSelectionsByChat: {} as Record<string, unknown[]> };
vi.mock('@/store/uiStore', () => {
  const useUIStore = (selector: (s: typeof uiState) => unknown) => selector(uiState);
  useUIStore.getState = () => ({
    clearComposerSelections: mocks.clearComposerSelections,
    removeComposerSelection: mocks.removeComposerSelection,
  });
  return { useUIStore };
});

vi.mock('@/store/messageQueueStore', () => ({
  useMessageQueueStore: { getState: () => ({ queueMessage: mocks.queueMessage }) },
}));

vi.mock('react-hot-toast', () => ({
  default: { error: mocks.toastError },
}));

// Heavy composer sub-trees that don't participate in submit/queue logic.
vi.mock('./InputControls', () => ({ InputControls: () => null }));
vi.mock('./ComposerUsage', () => ({ ComposerUsage: () => null }));
vi.mock('./ComposerAccount', () => ({ ComposerAccount: () => null }));
vi.mock('@/components/ui/FileUploadDialog/FileUploadDialog', () => ({
  FileUploadDialog: () => null,
}));
vi.mock('@/components/ui/drawing-modal/DrawingModal', () => ({ DrawingModal: () => null }));

// Suggestion/attachment/enhance hooks are unrelated to the send path — stub
// them so InputProvider renders without their real dependencies.
vi.mock('@/hooks/useInputAttachments', () => ({
  useInputAttachments: () => ({
    previewUrls: [],
    showFileUpload: false,
    setShowFileUpload: vi.fn(),
    showDrawingModal: false,
    editingImageIndex: null,
    handleFileSelect: vi.fn(),
    handleAddFiles: vi.fn(),
    handleRemoveFile: vi.fn(),
    handleDrawClick: vi.fn(),
    handleDrawingSave: vi.fn(),
    closeDrawingModal: vi.fn(),
    isDragging: false,
    dragHandlers: {},
    resetDragState: vi.fn(),
    handlePaste: vi.fn(),
  }),
}));

vi.mock('@/hooks/useInputEnhance', () => ({
  useInputEnhance: () => ({ isEnhancing: false, handleEnhancePrompt: vi.fn() }),
}));

vi.mock('@/hooks/useInputSuggestions', () => ({
  useInputSuggestions: () => ({
    slashCommandSuggestions: [],
    highlightedSlashCommandIndex: -1,
    selectSlashCommand: vi.fn(),
    handleSlashCommandKeyDown: () => false,
    filteredFiles: [],
    filteredChats: [],
    highlightedMentionIndex: -1,
    selectMention: vi.fn(),
    handleMentionKeyDown: () => false,
    isMentionActive: false,
  }),
}));

function renderInput(over: Partial<InputProps> = {}) {
  const onSubmit = vi.fn();
  const setMessage = vi.fn();
  const props: InputProps = {
    message: 'hello world',
    setMessage,
    onSubmit,
    isLoading: false,
    selectedModelId: 'claude-x',
    onModelChange: vi.fn(),
    chatId: 'chat-1',
    ...over,
  };
  render(<Input {...props} />);
  return { onSubmit, setMessage };
}

function pressEnter() {
  fireEvent.keyDown(screen.getByLabelText('Message input'), { key: 'Enter' });
}

describe('Input send/queue path', () => {
  beforeEach(() => {
    uiState.composerSelectionsByChat = {};
    vi.clearAllMocks();
  });
  afterEach(cleanup);

  it('submits the message on Enter when the agent is idle', () => {
    const { onSubmit } = renderInput({ isStreaming: false });
    pressEnter();
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(mocks.queueMessage).not.toHaveBeenCalled();
  });

  it('queues the message and clears the draft while a response is streaming', () => {
    const { onSubmit, setMessage } = renderInput({ isStreaming: true });
    pressEnter();

    expect(mocks.queueMessage).toHaveBeenCalledTimes(1);
    const [chatId, content, modelId] = mocks.queueMessage.mock.calls[0];
    expect(chatId).toBe('chat-1');
    expect(content).toBe('hello world');
    expect(modelId).toBe('claude-x');
    // The queued draft only clears because the dispatch was accepted.
    expect(setMessage).toHaveBeenCalledWith('');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('drops nothing: with no model selected it errors and keeps the draft instead of queuing', () => {
    const { onSubmit, setMessage } = renderInput({ isStreaming: true, selectedModelId: '' });
    pressEnter();

    expect(mocks.toastError).toHaveBeenCalledTimes(1);
    expect(mocks.queueMessage).not.toHaveBeenCalled();
    // Draft must survive — clearing without a successful queue would silently
    // lose the user's message.
    expect(setMessage).not.toHaveBeenCalledWith('');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it('offers a separate Stop button while streaming, even with a draft', () => {
    const onStopStream = vi.fn();
    const { setMessage } = renderInput({ isStreaming: true, onStopStream });
    expect(screen.getByRole('button', { name: 'Queue message' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Stop generating' }));
    expect(onStopStream).toHaveBeenCalledTimes(1);
    expect(mocks.queueMessage).not.toHaveBeenCalled();
    expect(setMessage).not.toHaveBeenCalled();
  });

  it('Esc stops the turn; Enter on an empty composer does not', () => {
    const onStopStream = vi.fn();
    renderInput({ isStreaming: true, onStopStream, message: '' });
    pressEnter();
    expect(onStopStream).not.toHaveBeenCalled();
    fireEvent.keyDown(screen.getByLabelText('Message input'), { key: 'Escape' });
    expect(onStopStream).toHaveBeenCalledTimes(1);
  });

  it('has no Stop button when idle', () => {
    renderInput({ isStreaming: false, onStopStream: vi.fn() });
    expect(screen.queryByRole('button', { name: 'Stop generating' })).toBeNull();
  });

  it('does not submit or queue an empty draft', () => {
    const { onSubmit } = renderInput({ isStreaming: false, message: '   ' });
    pressEnter();
    expect(onSubmit).not.toHaveBeenCalled();
    expect(mocks.queueMessage).not.toHaveBeenCalled();
  });
});
