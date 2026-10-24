// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, renderHook } from '@testing-library/react';
import {
  MAX_PERSISTED_DRAFTS,
  MAX_PERSISTED_DRAFT_CHARS,
  pruneDraftsForStorage,
  useComposerDraftStore,
} from './composerDraftStore';
import { useInputState } from '@/hooks/useInputState';

const persisted = () => {
  const raw = localStorage.getItem('composer-drafts');
  return raw ? (JSON.parse(raw).state.textByChat as Record<string, { text: string }>) : {};
};

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.clear();
  useComposerDraftStore.setState(useComposerDraftStore.getInitialState(), true);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('composer drafts', () => {
  it('keeps each chat its own draft across switches and clears on send', () => {
    const file = new File(['x'], 'a.png', { type: 'image/png' });
    const { result, rerender } = renderHook(({ chatId }) => useInputState({ chatId }), {
      initialProps: { chatId: 'chat-a' },
    });

    act(() => {
      result.current.setInputMessage('half-written for A');
      result.current.setInputFiles([file]);
    });
    rerender({ chatId: 'chat-b' });
    expect(result.current.inputMessage).toBe('');
    expect(result.current.inputFiles).toEqual([]);

    act(() => result.current.setInputMessage((prev) => `${prev}B`));
    rerender({ chatId: 'chat-a' });
    expect(result.current.inputMessage).toBe('half-written for A');
    expect(result.current.inputFiles).toEqual([file]);

    act(() => result.current.clearInput());
    expect(result.current.inputMessage).toBe('');
    expect(result.current.inputFiles).toEqual([]);
    expect(useComposerDraftStore.getState().textByChat['chat-b']?.text).toBe('B');
  });

  it('persists text (not files) once typing pauses', () => {
    const store = useComposerDraftStore.getState();
    store.setText('chat-a', 'hello');
    store.setFiles('chat-a', [new File(['x'], 'a.txt')]);
    expect(persisted()).toEqual({});

    vi.advanceTimersByTime(1000);
    expect(persisted()['chat-a']?.text).toBe('hello');
    expect(localStorage.getItem('composer-drafts')).not.toContain('a.txt');
  });

  it('flushes a pending write when the page is hidden', () => {
    useComposerDraftStore.getState().setText('chat-a', 'last words');
    window.dispatchEvent(new Event('pagehide'));
    expect(persisted()['chat-a']?.text).toBe('last words');
  });

  it('caps what goes to storage: newest drafts, no oversized ones', () => {
    const drafts: Record<string, { text: string; updatedAt: number }> = {};
    for (let i = 0; i < MAX_PERSISTED_DRAFTS + 5; i++) {
      drafts[`chat-${i}`] = { text: `draft ${i}`, updatedAt: i };
    }
    drafts.huge = { text: 'x'.repeat(MAX_PERSISTED_DRAFT_CHARS + 1), updatedAt: 1e9 };

    const kept = pruneDraftsForStorage(drafts);
    expect(Object.keys(kept)).toHaveLength(MAX_PERSISTED_DRAFTS);
    expect(kept.huge).toBeUndefined();
    expect(kept['chat-0']).toBeUndefined();
    expect(kept[`chat-${MAX_PERSISTED_DRAFTS + 4}`]).toBeDefined();
  });

  it('drops an emptied draft instead of storing blanks', () => {
    const store = useComposerDraftStore.getState();
    store.setText('chat-a', 'x');
    store.setText('chat-a', '');
    expect(useComposerDraftStore.getState().textByChat['chat-a']).toBeUndefined();
  });
});
