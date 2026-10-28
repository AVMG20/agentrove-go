import { useEffect, useState, type ReactNode } from 'react';
import { AppWindow, ArrowUpRight, Link2, Loader2, RotateCw, Sparkles, X } from 'lucide-react';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Dropdown } from '@/components/ui/primitives/Dropdown/Dropdown';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ArtifactFrame } from '@/components/artifacts/ArtifactFrame';
import { useArtifactViewQuery, useChatArtifactsQuery } from '@/hooks/queries/useArtifactQueries';
import { latestArtifact } from '@/hooks/useOpenArtifact';
import { artifactService } from '@/services/artifactService';
import { useUIStore } from '@/store/uiStore';
import type { Artifact } from '@/types/artifact.types';
import type { TileId } from '@/types/ui.types';
import { formatRelativeTime } from '@/utils/date';
import styles from './ArtifactPane.module.scss';

interface ArtifactPaneProps {
  chatId: string;
  tileId: TileId;
  isVisible: boolean;
}

// How long the header says "Updated to vN" after the agent publishes a version.
const UPDATED_FLASH_MS = 4_000;

function pick(chatId: string, artifactId: string) {
  useUIStore.setState((s) => ({ artifactByChat: { ...s.artifactByChat, [chatId]: artifactId } }));
}

// One published artifact beside the chat, live: a new version reloads it in
// place. The standalone /artifacts page stays one click away.
export function ArtifactPane({ chatId, tileId, isVisible }: ArtifactPaneProps) {
  const pickedId = useUIStore((s) => s.artifactByChat[chatId]);
  const { data: artifacts } = useChatArtifactsQuery(chatId);
  const latest = latestArtifact(artifacts);
  const artifactId = pickedId ?? latest?.id;
  const close = () => useUIStore.getState().removeTab(tileId);

  // Pin what's on screen so a newly published artifact doesn't swap it out
  // mid-read; it's offered in the header instead.
  useEffect(() => {
    if (!pickedId && latest) pick(chatId, latest.id);
  }, [chatId, pickedId, latest]);

  const newest = latestArtifact(artifacts)?.updated_at;
  useEffect(() => {
    if (isVisible && newest) useUIStore.getState().markArtifactsSeen(chatId, newest);
  }, [chatId, isVisible, newest]);

  if (!artifactId) {
    return (
      <Shell onClose={close}>
        <Status>
          {artifacts ? 'No artifacts in this thread yet.' : <Loader2 className={styles.spinner} />}
        </Status>
      </Shell>
    );
  }
  return (
    <ArtifactDetail
      chatId={chatId}
      artifactId={artifactId}
      artifacts={artifacts ?? []}
      isVisible={isVisible}
      onClose={close}
    />
  );
}

function ArtifactDetail({
  chatId,
  artifactId,
  artifacts,
  isVisible,
  onClose,
}: {
  chatId: string;
  artifactId: string;
  artifacts: Artifact[];
  isVisible: boolean;
  onClose: () => void;
}) {
  const { data: view, error, isLoading } = useArtifactViewQuery(artifactId, isVisible);
  const [reloadKey, setReloadKey] = useState(0);
  const updated = useUpdatedFlash(view?.id, view?.version);
  const fresh = useFreshArtifact(artifacts, artifactId);
  const listed = artifacts.find((a) => a.id === artifactId);
  const title = view?.title ?? listed?.title ?? 'Artifact';
  const href = `/artifacts/${artifactId}`;

  const copyLink = async () => {
    try {
      await navigator.clipboard.writeText(artifactService.getShareUrl(artifactId));
      toast.success(view?.is_public ? 'Link copied' : 'Link copied — only you can open it');
    } catch {
      toast.error('Failed to copy link');
    }
  };

  const header = (
    <>
      <div className={styles['header-text']}>
        {listed && artifacts.length > 1 ? (
          <div className={styles['switcher-slot']}>
            <Dropdown
              value={listed}
              items={artifacts}
              getItemKey={(a) => a.id}
              getItemLabel={(a) => a.title}
              onSelect={(a) => pick(chatId, a.id)}
              triggerClassName={styles.switcher}
              searchable={artifacts.length > 6}
              searchPlaceholder="Find an artifact"
              width="18rem"
            />
          </div>
        ) : (
          <h2 className={styles.title}>{title}</h2>
        )}
        {view && (
          <p className={styles.meta}>
            {updated ? (
              <span className={styles.updated}>Updated to v{view.version}</span>
            ) : (
              `v${view.version} · Updated ${formatRelativeTime(view.updated_at)}`
            )}
          </p>
        )}
      </div>
      {fresh && (
        <FloatingTooltip content="Show the new artifact">
          <Button
            variant="unstyled"
            type="button"
            className={styles.fresh}
            onClick={() => pick(chatId, fresh.id)}
          >
            <Sparkles className={styles['fresh-icon']} />
            <span className={styles['fresh-text']}>New: {fresh.title}</span>
          </Button>
        </FloatingTooltip>
      )}
      <IconButton label="Reload" onClick={() => setReloadKey((k) => k + 1)} disabled={!view}>
        <RotateCw className={styles.glyph} />
      </IconButton>
      <IconButton label="Copy link" onClick={() => void copyLink()}>
        <Link2 className={styles.glyph} />
      </IconButton>
      <FloatingTooltip content="Open in new tab">
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className={styles['icon-button']}
          aria-label="Open in new tab"
        >
          <ArrowUpRight className={styles.glyph} />
        </a>
      </FloatingTooltip>
    </>
  );

  return (
    <Shell header={header} onClose={onClose}>
      {view ? (
        <ArtifactFrame view={view} className={styles.frame} reloadKey={reloadKey} />
      ) : isLoading ? (
        <Status>
          <Loader2 className={styles.spinner} />
        </Status>
      ) : (
        <Status>
          <AppWindow className={styles['status-icon']} />
          <span>
            {error ? 'This artifact is no longer available.' : "Couldn't load this artifact."}
          </span>
          {artifacts.some((a) => a.id !== artifactId) && (
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                const other = latestArtifact(artifacts.filter((a) => a.id !== artifactId));
                if (other) pick(chatId, other.id);
              }}
            >
              Show latest
            </Button>
          )}
        </Status>
      )}
    </Shell>
  );
}

function Shell({
  header,
  onClose,
  children,
}: {
  header?: ReactNode;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <section className={styles.pane} aria-label="Artifact">
      <header className={styles.header}>
        {header ?? <h2 className={styles.title}>Artifacts</h2>}
        <IconButton label="Close" onClick={onClose}>
          <X className={styles.glyph} />
        </IconButton>
      </header>
      {children}
    </section>
  );
}

function Status({ children }: { children: ReactNode }) {
  return (
    <div className={styles.status} role="status" aria-live="polite">
      {children}
    </div>
  );
}

function IconButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <FloatingTooltip content={label}>
      <Button
        variant="unstyled"
        type="button"
        className={styles['icon-button']}
        onClick={onClick}
        disabled={disabled}
        aria-label={label}
      >
        {children}
      </Button>
    </FloatingTooltip>
  );
}

// True for a moment after the shown artifact gets a new version (not on load
// or when switching artifacts).
function useUpdatedFlash(id: string | undefined, version: number | undefined): boolean {
  const [flash, setFlash] = useState<{ id: string; version: number } | null>(null);
  const [prev, setPrev] = useState({ id, version });
  if (prev.id !== id || prev.version !== version) {
    setPrev({ id, version });
    if (id && id === prev.id && version !== undefined && prev.version !== undefined) {
      setFlash({ id, version });
    }
  }
  useEffect(() => {
    if (!flash) return;
    const timer = setTimeout(() => setFlash(null), UPDATED_FLASH_MS);
    return () => clearTimeout(timer);
  }, [flash]);
  return flash !== null && flash.id === id && flash.version === version;
}

// An artifact published after the pane opened, other than the one on screen.
function useFreshArtifact(artifacts: Artifact[], shownId: string): Artifact | undefined {
  const [known, setKnown] = useState<ReadonlySet<string> | null>(null);
  if (known === null) {
    if (artifacts.length > 0) setKnown(new Set([...artifacts.map((a) => a.id), shownId]));
    return undefined;
  }
  // Looking at it makes it known.
  if (!known.has(shownId)) setKnown(new Set([...known, shownId]));
  return latestArtifact(artifacts.filter((a) => !known.has(a.id) && a.id !== shownId));
}
