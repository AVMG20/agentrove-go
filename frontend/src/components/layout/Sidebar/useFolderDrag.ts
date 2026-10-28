import { useCallback, useState, type DragEvent } from 'react';
import type { FolderDropPlacement } from './sidebarGrouping';

// Own drag type, so dragged files, links or text never reorder folders.
const FOLDER_DRAG_TYPE = 'application/x-agentrove-folder';

export interface FolderDragProps {
  dragging: boolean;
  dropPlacement: FolderDropPlacement | null;
  // On the folder header (the drag handle).
  onDragStart: (e: DragEvent<HTMLElement>) => void;
  onDragEnd: () => void;
  // On the whole folder (the drop zone).
  onDragOver: (e: DragEvent<HTMLElement>) => void;
  onDragLeave: (e: DragEvent<HTMLElement>) => void;
  onDrop: (e: DragEvent<HTMLElement>) => void;
}

// Upper half of a folder drops before it, lower half after it.
function placementAt(e: DragEvent<HTMLElement>): FolderDropPlacement {
  const rect = e.currentTarget.getBoundingClientRect();
  return e.clientY < rect.top + rect.height / 2 ? 'before' : 'after';
}

// Native drag and drop of workspace folders by their header; returns the
// props for each folder.
export function useFolderDrag(
  onMove: (key: string, targetKey: string, placement: FolderDropPlacement) => void,
) {
  const [draggingKey, setDraggingKey] = useState<string | null>(null);
  const [dropTarget, setDropTarget] = useState<{
    key: string;
    placement: FolderDropPlacement;
  } | null>(null);

  const reset = useCallback(() => {
    setDraggingKey(null);
    setDropTarget(null);
  }, []);

  return (key: string): FolderDragProps => ({
    dragging: draggingKey === key,
    dropPlacement: dropTarget?.key === key ? dropTarget.placement : null,
    onDragStart: (e) => {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData(FOLDER_DRAG_TYPE, key);
      // Restyling the source during dragstart can cancel the drag in Chrome.
      requestAnimationFrame(() => setDraggingKey(key));
    },
    onDragEnd: reset,
    onDragOver: (e) => {
      if (!e.dataTransfer.types.includes(FOLDER_DRAG_TYPE)) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      // No line over the dragged folder itself: dropping there moves nothing.
      const placement = placementAt(e);
      setDropTarget((prev) => {
        if (key === draggingKey) return null;
        return prev?.key === key && prev.placement === placement ? prev : { key, placement };
      });
    },
    onDragLeave: (e) => {
      // Leaving into a child still counts as over this folder.
      if (e.currentTarget.contains(e.relatedTarget as Node | null)) return;
      setDropTarget((prev) => (prev?.key === key ? null : prev));
    },
    onDrop: (e) => {
      const dragged = e.dataTransfer.getData(FOLDER_DRAG_TYPE);
      if (!dragged) return;
      e.preventDefault();
      const placement = placementAt(e);
      reset();
      if (dragged !== key) onMove(dragged, key, placement);
    },
  });
}
