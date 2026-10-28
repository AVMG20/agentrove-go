import { useIsMobile } from '@/hooks/useIsMobile';
import { useUIStore } from '@/store/uiStore';
import { paintedLayout, viewTypeToTileId } from '@/utils/tileHelpers';
import type { Artifact } from '@/types/artifact.types';
import type { SplitSlot, TileId } from '@/types/ui.types';

export function artifactTileFor(chatId: string, splitChatIds: string[]): TileId {
  const index = splitChatIds.indexOf(chatId);
  return viewTypeToTileId('artifact', index >= 0 ? ((index + 1) as SplitSlot) : null);
}

// What an artifact pane shows without an explicit pick: the latest edit.
export function latestArtifact(artifacts: Artifact[] | undefined): Artifact | undefined {
  return artifacts?.reduce<Artifact | undefined>(
    (latest, a) => (!latest || a.updated_at > latest.updated_at ? a : latest),
    undefined,
  );
}

// The artifact on screen in this chat's pane (null: the pane is closed). An
// unpicked pane resolves to the latest, which callers pass in.
export function useOpenArtifactId(chatId: string, fallbackId?: string): string | null {
  const isMobile = useIsMobile();
  return useUIStore((s) => {
    const tile = artifactTileFor(chatId, s.splitChatIds);
    const shown = paintedLayout(s.visibleLayout, s.focusedTile, isMobile).flat().includes(tile);
    return shown ? (s.artifactByChat[chatId] ?? fallbackId ?? null) : null;
  });
}
