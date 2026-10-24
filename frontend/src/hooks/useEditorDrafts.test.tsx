// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useEditorDrafts } from './useEditorDrafts';
import type { FileStructure } from '@/types/file-system.types';

const file: FileStructure = { path: 'src/app.ts', content: '', type: 'file' };

function unloadPrevented(): boolean {
  const event = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(event);
  return event.defaultPrevented;
}

describe('useEditorDrafts unload guard', () => {
  it('asks before leaving only while a buffer is unsaved', () => {
    const { result } = renderHook(() =>
      useEditorDrafts({
        selectedFile: file,
        selectedFileContent: 'saved',
        sandboxId: 'sb-1',
        onCloseFile: vi.fn(),
      }),
    );
    expect(unloadPrevented()).toBe(false);

    act(() => result.current.handleEditorChange('edited'));
    expect(unloadPrevented()).toBe(true);

    act(() => result.current.commitSave('src/app.ts', 'edited'));
    expect(unloadPrevented()).toBe(false);
  });
});
