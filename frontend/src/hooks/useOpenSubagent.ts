import { useIsMobile } from '@/hooks/useIsMobile';
import { useUIStore } from '@/store/uiStore';
import { paintedLayout, viewTypeToTileId } from '@/utils/tileHelpers';
import type { SplitSlot, TileId } from '@/types/ui.types';

export function subagentTileFor(chatId: string, splitChatIds: string[]): TileId {
  const index = splitChatIds.indexOf(chatId);
  return viewTypeToTileId('subagent', index >= 0 ? ((index + 1) as SplitSlot) : null);
}

// The subagent whose pane is on screen for this chat, if any.
export function useOpenSubagentId(chatId: string): string | undefined {
  const isMobile = useIsMobile();
  return useUIStore((s) => {
    const id = s.subagentByChat[chatId];
    if (!id) return undefined;
    const tile = subagentTileFor(chatId, s.splitChatIds);
    const shown = paintedLayout(s.visibleLayout, s.focusedTile, isMobile).flat().includes(tile);
    return shown ? id : undefined;
  });
}
