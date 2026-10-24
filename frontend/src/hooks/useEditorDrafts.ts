import { useState, useRef, useCallback, useEffect } from 'react';
import type { FileStructure } from '@/types/file-system.types';

interface UseEditorDraftsArgs {
  selectedFile: FileStructure | null;
  selectedFileContent: string | undefined;
  // Version of selectedFileContent on disk (see the file content response).
  selectedFileVersion?: string;
  sandboxId: string | undefined;
  onCloseFile: (path: string) => void;
}

// Session-scoped unsaved buffers (not persisted). Tab switches keep drafts; sandbox/chat switch drops them.
export function useEditorDrafts({
  selectedFile,
  selectedFileContent,
  selectedFileVersion,
  sandboxId,
  onCloseFile,
}: UseEditorDraftsArgs) {
  const [currentContent, setCurrentContent] = useState('');
  // Active file + saved baseline the live buffer diffs against.
  const prevSelectedFileRef = useRef<FileStructure | null>(null);
  const draftsRef = useRef<Map<string, string>>(new Map());
  // Disk version of the loaded baseline, and the version each draft started
  // from: a refetch rebases the buffer silently, but the save must still be
  // checked against what the user actually edited.
  const loadedVersionRef = useRef<{ path: string; version?: string } | null>(null);
  const draftBaseRef = useRef<Map<string, string | undefined>>(new Map());
  // Mirrored in state so every tab can show its own unsaved dot.
  const [dirtyPaths, setDirtyPaths] = useState<Set<string>>(() => new Set());
  const [pendingClosePath, setPendingClosePath] = useState<string | null>(null);
  const prevSandboxIdRef = useRef(sandboxId);
  const selectedFilePath = selectedFile?.path ?? null;

  if (prevSandboxIdRef.current !== sandboxId) {
    // Pane isn't remounted across chats; drafts are path-keyed only — drop them so
    // same-path files don't bleed across sandboxes. Null prev so content effect re-inits.
    prevSandboxIdRef.current = sandboxId;
    draftsRef.current.clear();
    draftBaseRef.current.clear();
    prevSelectedFileRef.current = null;
    if (dirtyPaths.size > 0) setDirtyPaths(new Set());
    if (pendingClosePath !== null) setPendingClosePath(null);
  }

  const hasUnsavedChanges = !!selectedFilePath && dirtyPaths.has(selectedFilePath);
  const hasLoadedSelectedFile = prevSelectedFileRef.current?.path === selectedFile?.path;

  // Single writer keeps draftsRef and dirtyPaths in lockstep; null clears the draft.
  const setDraft = useCallback((path: string, content: string | null) => {
    const isDirty = content !== null;
    if (isDirty) {
      draftsRef.current.set(path, content);
      if (!draftBaseRef.current.has(path)) {
        const loaded = loadedVersionRef.current;
        draftBaseRef.current.set(path, loaded?.path === path ? loaded.version : undefined);
      }
    } else {
      draftsRef.current.delete(path);
      draftBaseRef.current.delete(path);
    }
    setDirtyPaths((prev) => {
      if (prev.has(path) === isDirty) return prev;
      const next = new Set(prev);
      if (isDirty) next.add(path);
      else next.delete(path);
      return next;
    });
  }, []);

  useEffect(() => {
    if (!selectedFile) {
      // Drop baseline so reopening the same path reloads from the server, not stale buffer.
      prevSelectedFileRef.current = null;
      setCurrentContent('');
      return;
    }

    const prev = prevSelectedFileRef.current;
    const fileChanged = !prev || prev.path !== selectedFile.path;
    if (selectedFileContent !== undefined) {
      loadedVersionRef.current = { path: selectedFile.path, version: selectedFileVersion };
    }

    const queryContentChanged =
      selectedFileContent !== undefined &&
      prev?.path === selectedFile.path &&
      prev?.content !== selectedFileContent;

    if (fileChanged || queryContentChanged) {
      const baseline = selectedFileContent ?? '';
      // User draft wins over refetch; only read once content loaded (avoid racing the fetch).
      const draft =
        selectedFileContent !== undefined ? draftsRef.current.get(selectedFile.path) : undefined;
      // Agent made the same edit — draft matches baseline, no longer dirty.
      if (draft !== undefined && draft === baseline) {
        setDraft(selectedFile.path, null);
      }

      prevSelectedFileRef.current = {
        ...selectedFile,
        content: baseline,
      };

      setCurrentContent(draft ?? baseline);
    }
  }, [selectedFile, selectedFileContent, selectedFileVersion, setDraft]);

  // Selection updates before the content effect; hide previous file's text for that render.
  const displayContent = hasLoadedSelectedFile ? currentContent : (selectedFileContent ?? '');

  const handleEditorChange = useCallback(
    (value: string | undefined) => {
      if (value === undefined) return;

      setCurrentContent(value);

      const baseline = prevSelectedFileRef.current?.content ?? '';
      const path = prevSelectedFileRef.current?.path;
      if (path) setDraft(path, value !== baseline ? value : null);
    },
    [setDraft],
  );

  // Unsaved buffers live only in memory: ask before a reload or tab close drops them.
  const hasDirtyBuffers = dirtyPaths.size > 0;
  useEffect(() => {
    if (!hasDirtyBuffers) return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      // Older browsers need returnValue set to show the prompt.
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [hasDirtyBuffers]);

  // Dirty close prompts first — draft is session-only and would be discarded silently.
  const handleCloseTab = useCallback(
    (path: string) => {
      if (dirtyPaths.has(path)) setPendingClosePath(path);
      else onCloseFile(path);
    },
    [dirtyPaths, onCloseFile],
  );

  const confirmCloseTab = useCallback(() => {
    if (pendingClosePath === null) return;
    setDraft(pendingClosePath, null);
    onCloseFile(pendingClosePath);
    setPendingClosePath(null);
  }, [pendingClosePath, setDraft, onCloseFile]);

  const cancelCloseTab = useCallback(() => setPendingClosePath(null), []);

  // Rebaseline the saved path (not the now-active tab) so further edits diff against disk.
  const commitSave = useCallback(
    (savedPath: string, submitted: string, version?: string) => {
      if (prevSelectedFileRef.current?.path === savedPath) {
        prevSelectedFileRef.current = {
          ...prevSelectedFileRef.current,
          content: submitted,
        };
        loadedVersionRef.current = { path: savedPath, version };
      }
      setDraft(savedPath, null);
    },
    [setDraft],
  );

  // The disk version the path's edits are based on, for the save's conflict check.
  const baseVersionFor = useCallback((path: string) => {
    if (draftBaseRef.current.has(path)) return draftBaseRef.current.get(path);
    const loaded = loadedVersionRef.current;
    return loaded?.path === path ? loaded.version : undefined;
  }, []);

  // Drop the draft and show the loaded baseline ("reload from disk").
  const discardDraft = useCallback(
    (path: string) => {
      setDraft(path, null);
      if (prevSelectedFileRef.current?.path === path) {
        setCurrentContent(prevSelectedFileRef.current.content);
      }
    },
    [setDraft],
  );

  return {
    currentContent,
    displayContent,
    hasUnsavedChanges,
    hasLoadedSelectedFile,
    dirtyPaths,
    pendingClosePath,
    handleEditorChange,
    handleCloseTab,
    confirmCloseTab,
    cancelCloseTab,
    commitSave,
    baseVersionFor,
    discardDraft,
  };
}
