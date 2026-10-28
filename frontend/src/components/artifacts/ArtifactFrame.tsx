import { useRef, useState } from 'react';
import { artifactService } from '@/services/artifactService';
import type { ArtifactView } from '@/types/artifact.types';
import { useArtifactBridge } from './useArtifactBridge';

// No allow-same-origin: the page gets an opaque origin, so its scripts can't
// reach this app's storage, cookies or API. The content response repeats this
// as a CSP sandbox for when the page is opened on its own.
const FRAME_SANDBOX =
  'allow-scripts allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-downloads';

interface ArtifactFrameProps {
  view: ArtifactView;
  className?: string;
  // Bump to reload the page on demand (fresh view token included).
  reloadKey?: number;
}

// The sandboxed artifact page plus its viewer bridge; shared by the standalone
// /artifacts page and the pane beside the chat.
export function ArtifactFrame({ view, className, reloadKey = 0 }: ArtifactFrameProps) {
  const frameRef = useRef<HTMLIFrameElement>(null);
  useArtifactBridge(frameRef, view);

  // Re-point the iframe only when the content changes: every refetch mints a
  // fresh view token (and visibility toggles add or drop one), and a new src
  // would reload the page and throw away whatever state it holds.
  const frameKey = `${view.id}:${view.version}:${reloadKey}`;
  const [frame, setFrame] = useState<{ key: string; src: string } | null>(null);
  let current = frame;
  if (frameKey !== frame?.key) {
    current = { key: frameKey, src: artifactService.getContentUrl(view) };
    setFrame(current);
  }

  return (
    <iframe
      key={current?.key}
      ref={frameRef}
      className={className}
      src={current?.src}
      sandbox={FRAME_SANDBOX}
      referrerPolicy="no-referrer"
      title={view.title}
    />
  );
}
