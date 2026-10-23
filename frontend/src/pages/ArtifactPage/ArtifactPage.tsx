import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import clsx from 'clsx';
import toast from 'react-hot-toast';
import { ExternalLink, Globe, Link2, Loader2, Lock } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Switch } from '@/components/ui/primitives/Switch/Switch';
import {
  useArtifactViewQuery,
  useUpdateArtifactMutation,
} from '@/hooks/queries/useArtifactQueries';
import { artifactService } from '@/services/artifactService';
import { authService } from '@/services/authService';
import { useAuthStore } from '@/store/authStore';
import type { ServiceError } from '@/services/base/ServiceError';
import { formatRelativeTime } from '@/utils/date';
import iconDark from '/assets/images/icon-dark.svg';
import iconLight from '/assets/images/icon-white.svg';
import styles from './ArtifactPage.module.scss';

// No allow-same-origin: the page gets an opaque origin, so its scripts can't
// reach this app's storage, cookies or API. The content response repeats this
// as a CSP sandbox for when the page is opened on its own.
const FRAME_SANDBOX =
  'allow-scripts allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-downloads';

export function ArtifactPage() {
  const { artifactId } = useParams<{ artifactId: string }>();
  const navigate = useNavigate();
  const isAuthenticated =
    useAuthStore((state) => state.isAuthenticated) && !!authService.getToken();
  const { data: view, error, isLoading } = useArtifactViewQuery(artifactId);
  const updateArtifact = useUpdateArtifactMutation();

  // Re-point the iframe only when the content changes: every refetch mints a
  // fresh view token (and visibility toggles add or drop one), and a new src
  // would reload the page and throw away whatever state it holds.
  const frameKey = view ? `${view.id}:${view.version}` : null;
  const [frame, setFrame] = useState<{ key: string; src: string } | null>(null);
  if (view && frameKey && frameKey !== frame?.key) {
    setFrame({ key: frameKey, src: artifactService.getContentUrl(view) });
  }

  const handleVisibilityChange = async (isPublic: boolean) => {
    if (!view) return;
    try {
      await updateArtifact.mutateAsync({ artifactId: view.id, data: { is_public: isPublic } });
      toast.success(isPublic ? 'Anyone with the link can view it' : 'Only you can view it');
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Failed to change visibility');
    }
  };

  const handleCopyLink = async () => {
    if (!view) return;
    try {
      await navigator.clipboard.writeText(artifactService.getShareUrl(view.id));
      toast.success(view.is_public ? 'Link copied' : 'Link copied — only you can open it');
    } catch {
      toast.error('Failed to copy link');
    }
  };

  if (isLoading) {
    return (
      <div className={styles.page}>
        <div className={styles.status} role="status" aria-live="polite">
          <Loader2 className={styles.spinner} />
        </div>
      </div>
    );
  }

  if (!view || !frame) {
    const notFound = (error as ServiceError | null)?.status === 404;
    return (
      <div className={styles.page}>
        <div className={styles.status}>
          <Lock className={styles['status-icon']} />
          <h1 className={styles['status-title']}>
            {notFound ? 'Artifact unavailable' : "Couldn't load this artifact"}
          </h1>
          <p className={styles['status-text']}>
            {!notFound
              ? 'Something went wrong. Try again in a moment.'
              : isAuthenticated
                ? "It was deleted, or it's private and belongs to someone else."
                : "It's private or no longer exists. Sign in if it's yours."}
          </p>
          {notFound && !isAuthenticated ? (
            <Button
              size="sm"
              onClick={() =>
                navigate(`/login?next=${encodeURIComponent(`/artifacts/${artifactId}`)}`)
              }
            >
              Sign in
            </Button>
          ) : (
            <Button size="sm" variant="outline" onClick={() => navigate('/')}>
              Go to Agentrove
            </Button>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className={styles.page}>
      <header className={styles.bar}>
        <Link to="/" className={styles.home} aria-label="Go to Agentrove" title="Agentrove">
          <img src={iconDark} alt="" className={clsx(styles.logo, styles['logo--on-light'])} />
          <img src={iconLight} alt="" className={clsx(styles.logo, styles['logo--on-dark'])} />
        </Link>
        <div className={styles.heading}>
          <h1 className={styles.title}>{view.title}</h1>
          <span className={styles.meta}>
            v{view.version} · Updated {formatRelativeTime(view.updated_at)}
          </span>
        </div>
        <div className={styles.actions}>
          {view.is_owner ? (
            <label className={styles.visibility}>
              {view.is_public ? (
                <Globe className={styles['visibility-icon']} />
              ) : (
                <Lock className={styles['visibility-icon']} />
              )}
              <span className={styles['visibility-label']}>
                {view.is_public ? 'Public' : 'Private'}
              </span>
              <Switch
                size="sm"
                checked={view.is_public}
                disabled={updateArtifact.isPending}
                onCheckedChange={(checked) => void handleVisibilityChange(checked)}
                aria-label={view.is_public ? 'Make private' : 'Make public'}
              />
            </label>
          ) : null}
          <Button
            variant="ghost"
            size="icon"
            className={styles['action-btn']}
            onClick={() => void handleCopyLink()}
            aria-label="Copy link"
            title="Copy link"
          >
            <Link2 className={styles['action-icon']} />
          </Button>
          <a
            href={frame.src}
            target="_blank"
            rel="noopener noreferrer"
            className={styles['action-btn']}
            aria-label="Open in new tab"
            title="Open in new tab"
          >
            <ExternalLink className={styles['action-icon']} />
          </a>
        </div>
      </header>
      <iframe
        key={frame.key}
        className={styles.frame}
        src={frame.src}
        sandbox={FRAME_SANDBOX}
        referrerPolicy="no-referrer"
        title={view.title}
      />
    </div>
  );
}
