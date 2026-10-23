import { useCallback } from 'react';
import { AppWindow, Globe, Link2, Lock } from 'lucide-react';
import { Link } from 'react-router-dom';
import toast from 'react-hot-toast';
import { ListManagementTab } from '@/components/ui/ListManagementTab/ListManagementTab';
import { Switch } from '@/components/ui/primitives/Switch/Switch';
import {
  useArtifactsQuery,
  useDeleteArtifactMutation,
  useUpdateArtifactMutation,
} from '@/hooks/queries/useArtifactQueries';
import type { Artifact } from '@/types/artifact.types';
import { formatRelativeTime } from '@/utils/date';
import { artifactService } from '@/services/artifactService';
import styles from './ArtifactsSettingsTab.module.scss';

export function ArtifactsSettingsTab() {
  const { data: artifacts } = useArtifactsQuery();
  const deleteArtifact = useDeleteArtifactMutation();
  const updateArtifact = useUpdateArtifactMutation();
  const items = artifacts ?? null;

  const handleDelete = useCallback(
    async (index: number) => {
      const artifact = items?.[index];
      if (!artifact) return;
      try {
        await deleteArtifact.mutateAsync(artifact.id);
        toast.success('Artifact deleted');
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to delete artifact');
        throw error;
      }
    },
    [items, deleteArtifact],
  );

  const handleVisibility = useCallback(
    async (artifact: Artifact, isPublic: boolean) => {
      try {
        await updateArtifact.mutateAsync({
          artifactId: artifact.id,
          data: { is_public: isPublic },
        });
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to change visibility');
      }
    },
    [updateArtifact],
  );

  const handleCopyLink = useCallback(async (artifact: Artifact) => {
    try {
      await navigator.clipboard.writeText(artifactService.getShareUrl(artifact.id));
      toast.success(artifact.is_public ? 'Link copied' : 'Link copied — only you can open it');
    } catch {
      toast.error('Failed to copy link');
    }
  }, []);

  return (
    <ListManagementTab<Artifact>
      title="Artifacts"
      description="Pages agents published for you. Private ones open only for you while signed in; public ones open for anyone with the link."
      items={items}
      emptyIcon={AppWindow}
      emptyText="No artifacts yet — ask an agent to build a page"
      deleteConfirmTitle="Delete artifact"
      deleteConfirmMessage={(artifact) =>
        `Delete "${artifact.title}"? Its link stops working for everyone. This can't be undone.`
      }
      getItemKey={(artifact) => artifact.id}
      onDelete={handleDelete}
      renderItem={(artifact) => (
        <>
          <div className={styles['item-header']}>
            {artifact.is_public ? (
              <Globe className={styles['visibility-icon']} aria-hidden="true" />
            ) : (
              <Lock className={styles['visibility-icon']} aria-hidden="true" />
            )}
            <h3 className={styles['item-title']}>
              <Link to={`/artifacts/${artifact.id}`} className={styles['title-link']}>
                {artifact.title}
              </Link>
            </h3>
          </div>
          <div className={styles['item-meta']}>
            <span>{artifact.is_public ? 'Public' : 'Private'}</span>
            <span>v{artifact.version}</span>
            <span className={styles.muted}>Updated {formatRelativeTime(artifact.updated_at)}</span>
          </div>
        </>
      )}
      renderItemActions={(artifact) => (
        <>
          <button
            type="button"
            onClick={() => void handleCopyLink(artifact)}
            className={styles['icon-button']}
            aria-label="Copy link"
            title="Copy link"
          >
            <Link2 className={styles['icon']} />
          </button>
          <Switch
            size="sm"
            className={styles.toggle}
            checked={artifact.is_public}
            onCheckedChange={(isPublic) => void handleVisibility(artifact, isPublic)}
            aria-label={artifact.is_public ? 'Make private' : 'Make public'}
            title={
              artifact.is_public
                ? 'Public — click to make private'
                : 'Private — click to make public'
            }
          />
        </>
      )}
      logContext="ArtifactsSettingsTab"
    />
  );
}
