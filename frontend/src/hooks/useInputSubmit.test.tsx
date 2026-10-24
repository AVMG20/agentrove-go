// @vitest-environment jsdom

import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import type { ComposerSelection } from '@/store/uiStore';

const h = vi.hoisted(() => ({
  queueMessage: vi.fn<(...args: unknown[]) => Promise<void>>(() => Promise.resolve()),
  clearComposerSelections: vi.fn(),
  removeComposerSelection: vi.fn(),
  toastError: vi.fn(),
  settings: {
    permissionModeByChat: {} as Record<string, unknown>,
    thinkingModeByChat: {} as Record<string, unknown>,
    worktreeByChat: {} as Record<string, unknown>,
    worktreeBaseBranchByChat: {} as Record<string, string | undefined>,
    personaByChat: {} as Record<string, unknown>,
  },
}));

vi.mock('react-hot-toast', () => ({ default: { error: h.toastError } }));
vi.mock('@/store/messageQueueStore', () => ({
  useMessageQueueStore: { getState: () => ({ queueMessage: h.queueMessage }) },
}));
vi.mock('@/store/uiStore', () => ({
  useUIStore: {
    getState: () => ({
      clearComposerSelections: h.clearComposerSelections,
      removeComposerSelection: h.removeComposerSelection,
    }),
  },
}));
vi.mock('@/store/chatSettingsStore', () => ({
  useChatSettingsStore: {
    getState: () => h.settings,
  },
  DEFAULT_PERMISSION_MODE: 'bypassPermissions',
  DEFAULT_THINKING_MODE: 'high',
  DEFAULT_WORKTREE: false,
  DEFAULT_PERSONA: 'Default',
}));

import { useInputSubmit } from './useInputSubmit';

function makeOptions(overrides: Record<string, unknown> = {}) {
  return {
    disabled: false,
    hasContent: true,
    onSubmit: vi.fn(),
    setPreviewDismissed: vi.fn(),
    isStreaming: false,
    onStopStream: vi.fn(),
    isLoading: false,
    chatId: 'chat-1',
    selectedModelId: 'model-1',
    selectedModel: undefined,
    personas: [],
    attachedSelections: [] as ComposerSelection[],
    attachedFiles: null,
    messageRef: { current: 'hello' },
    setMessage: vi.fn(),
    onAttach: vi.fn(),
    formRef: { current: null } as React.RefObject<HTMLFormElement | null>,
    textareaRef: { current: null } as React.RefObject<HTMLTextAreaElement | null>,
    handleMentionKeyDown: vi.fn(() => false),
    handleSlashCommandKeyDown: vi.fn(() => false),
    ...overrides,
  } as Parameters<typeof useInputSubmit>[0];
}

function keyEvent(key: string, extra: Record<string, unknown> = {}) {
  return {
    key,
    shiftKey: false,
    metaKey: false,
    ctrlKey: false,
    defaultPrevented: false,
    nativeEvent: { isComposing: false },
    preventDefault: vi.fn(),
    ...extra,
  } as unknown as React.KeyboardEvent<Element>;
}

function mockCoarsePointer(coarse: boolean) {
  window.matchMedia = vi.fn((query: string) => ({
    matches: coarse && query === '(pointer: coarse)',
  })) as unknown as typeof window.matchMedia;
}

function formEvent() {
  return { preventDefault: vi.fn() } as unknown as React.FormEvent;
}

beforeEach(() => {
  vi.clearAllMocks();
  mockCoarsePointer(false);
  h.settings.permissionModeByChat = {};
  h.settings.thinkingModeByChat = {};
  h.settings.worktreeByChat = {};
  h.settings.worktreeBaseBranchByChat = {};
  h.settings.personaByChat = {};
});

describe('useInputSubmit — handleSubmit', () => {
  it('submits content and dismisses the preview', () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = formEvent();
    act(() => result.current.handleSubmit(e));
    expect(e.preventDefault).toHaveBeenCalled();
    expect(opts.setPreviewDismissed).toHaveBeenCalledWith(true);
    expect(opts.onSubmit).toHaveBeenCalledWith(e);
  });

  it('bails when disabled', () => {
    const opts = makeOptions({ disabled: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleSubmit(formEvent()));
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });

  it('bails when there is no content', () => {
    const opts = makeOptions({ hasContent: false });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleSubmit(formEvent()));
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });
});

describe('useInputSubmit — stopping', () => {
  it('Enter on an empty composer does nothing while streaming', () => {
    const opts = makeOptions({ isStreaming: true, hasContent: false });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleKeyDown(keyEvent('Enter')));
    expect(opts.onStopStream).not.toHaveBeenCalled();
    expect(h.queueMessage).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });

  it('Esc stops the running turn and keeps the draft', () => {
    const opts = makeOptions({ isStreaming: true, hasContent: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Escape');
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).toHaveBeenCalled();
    expect(opts.onStopStream).toHaveBeenCalledTimes(1);
    expect(opts.setMessage).not.toHaveBeenCalled();
  });

  it('Esc stops a pending start', () => {
    const opts = makeOptions({ isLoading: true, hasContent: false });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleKeyDown(keyEvent('Escape')));
    expect(opts.onStopStream).toHaveBeenCalledTimes(1);
  });

  it('Esc is left alone when idle, or when a menu already handled it', () => {
    const idle = makeOptions();
    const { result: idleResult } = renderHook(() => useInputSubmit(idle));
    const idleEvent = keyEvent('Escape');
    act(() => idleResult.current.handleKeyDown(idleEvent));
    expect(idle.onStopStream).not.toHaveBeenCalled();
    expect(idleEvent.preventDefault).not.toHaveBeenCalled();

    const menu = makeOptions({ isStreaming: true, handleMentionKeyDown: vi.fn(() => true) });
    const { result: menuResult } = renderHook(() => useInputSubmit(menu));
    act(() => menuResult.current.handleKeyDown(keyEvent('Escape')));
    expect(menu.onStopStream).not.toHaveBeenCalled();
  });

  it('Esc does not stop the turn behind an open dialog', () => {
    const dialog = document.createElement('div');
    dialog.setAttribute('aria-modal', 'true');
    document.body.appendChild(dialog);
    try {
      const opts = makeOptions({ isStreaming: true });
      const { result } = renderHook(() => useInputSubmit(opts));
      act(() => result.current.handleKeyDown(keyEvent('Escape')));
      expect(opts.onStopStream).not.toHaveBeenCalled();
    } finally {
      dialog.remove();
    }
  });

  it('the Stop button stops even with a draft in the composer', () => {
    const opts = makeOptions({ isStreaming: true, hasContent: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleStopClick({ preventDefault: vi.fn() } as never));
    expect(opts.onStopStream).toHaveBeenCalledTimes(1);
    expect(h.queueMessage).not.toHaveBeenCalled();
  });

  it('sending while a start is pending does nothing', () => {
    const opts = makeOptions({ isLoading: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());
    expect(opts.onStopStream).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });
});

describe('useInputSubmit — submitOrQueue queue path', () => {
  it('queues the message, clears the draft, and dismisses the preview', () => {
    const opts = makeOptions({ isStreaming: true, hasContent: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());

    expect(h.queueMessage).toHaveBeenCalledTimes(1);
    const [chatId, message, modelId] = h.queueMessage.mock.calls[0];
    expect(chatId).toBe('chat-1');
    expect(message).toBe('hello');
    expect(modelId).toBe('model-1');
    expect(opts.setMessage).toHaveBeenCalledWith('');
    expect(opts.onAttach).toHaveBeenCalledWith([]);
    expect(opts.setPreviewDismissed).toHaveBeenCalledWith(true);
  });

  it('threads the per-chat worktree and base branch into the queued options', () => {
    h.settings.worktreeByChat = { 'chat-1': true };
    h.settings.worktreeBaseBranchByChat = { 'chat-1': 'develop' };
    const opts = makeOptions({ isStreaming: true, hasContent: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());

    const options = h.queueMessage.mock.calls[0][3] as { worktree: boolean; baseBranch?: string };
    expect(options.worktree).toBe(true);
    expect(options.baseBranch).toBe('develop');
  });

  it('refuses to queue and warns when no model is selected — the draft survives', () => {
    const opts = makeOptions({ isStreaming: true, hasContent: true, selectedModelId: '' });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());

    expect(h.toastError).toHaveBeenCalledWith('Please select an AI model');
    expect(h.queueMessage).not.toHaveBeenCalled();
    expect(opts.setMessage).not.toHaveBeenCalled();
  });

  it('clears composer selections only when some are attached', () => {
    const selections = [{ text: 'foo.ts', kind: 'file' }] as unknown as ComposerSelection[];
    const opts = makeOptions({
      isStreaming: true,
      hasContent: true,
      attachedSelections: selections,
    });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());
    expect(h.clearComposerSelections).toHaveBeenCalledWith('chat-1');
  });
});

describe('useInputSubmit — submitOrQueue send path', () => {
  it('requestSubmits the form when idle with content', () => {
    const requestSubmit = vi.fn();
    const form = { requestSubmit } as unknown as HTMLFormElement;
    const opts = makeOptions({ formRef: { current: form } });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());
    expect(requestSubmit).toHaveBeenCalledTimes(1);
    expect(opts.setPreviewDismissed).toHaveBeenCalledWith(true);
  });

  it('falls back to a synthetic submit event when the form lacks requestSubmit', () => {
    const opts = makeOptions({ formRef: { current: null } });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());
    expect(opts.onSubmit).toHaveBeenCalledTimes(1);
  });

  it('does nothing when disabled and not streaming', () => {
    const opts = makeOptions({ disabled: true });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.submitOrQueue());
    expect(opts.onSubmit).not.toHaveBeenCalled();
    expect(opts.onStopStream).not.toHaveBeenCalled();
  });
});

describe('useInputSubmit — keyboard handling', () => {
  it('Enter without shift submits; suggestion handlers get first refusal', () => {
    const opts = makeOptions({
      formRef: { current: { requestSubmit: vi.fn() } as unknown as HTMLFormElement },
    });
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter');
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).toHaveBeenCalled();
    expect(opts.handleMentionKeyDown).toHaveBeenCalled();
    expect(opts.handleSlashCommandKeyDown).toHaveBeenCalled();
  });

  it('lets a mention-menu handler swallow Enter without submitting', () => {
    const opts = makeOptions({ handleMentionKeyDown: vi.fn(() => true) });
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter');
    act(() => result.current.handleKeyDown(e));
    expect(opts.handleSlashCommandKeyDown).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });

  it('Shift+Enter inserts a newline (no submit)', () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter', { shiftKey: true });
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });

  it('Enter while an IME is composing confirms the composition, not the message', () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter', { nativeEvent: { isComposing: true } });
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });
});

describe('useInputSubmit — touch keyboards', () => {
  it('Return inserts a newline on a coarse pointer', () => {
    mockCoarsePointer(true);
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter');
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).not.toHaveBeenCalled();
    expect(opts.onSubmit).not.toHaveBeenCalled();
  });

  it('Cmd/Ctrl+Return still sends from a hardware keyboard', () => {
    mockCoarsePointer(true);
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    const e = keyEvent('Enter', { metaKey: true });
    act(() => result.current.handleKeyDown(e));
    expect(e.preventDefault).toHaveBeenCalled();
    expect(opts.onSubmit).toHaveBeenCalledTimes(1);
  });

  it('the send button sends on a coarse pointer', () => {
    mockCoarsePointer(true);
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleSendClick({ preventDefault: vi.fn() } as never));
    expect(opts.onSubmit).toHaveBeenCalledTimes(1);
  });
});

describe('useInputSubmit — selection removal', () => {
  it('removes a selection by index for the active chat', () => {
    const opts = makeOptions();
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleRemoveSelection(2));
    expect(h.removeComposerSelection).toHaveBeenCalledWith('chat-1', 2);
  });

  it('is a no-op without a chatId', () => {
    const opts = makeOptions({ chatId: undefined });
    const { result } = renderHook(() => useInputSubmit(opts));
    act(() => result.current.handleRemoveSelection(0));
    expect(h.removeComposerSelection).not.toHaveBeenCalled();
  });
});
