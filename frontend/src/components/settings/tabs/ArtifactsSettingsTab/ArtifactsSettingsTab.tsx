import { useCallback, useMemo, useState } from 'react';
import { AppWindow, Globe, Link2, Lock, Pin, PinOff, Search } from 'lucide-react';
import clsx from 'clsx';
import { Link } from 'react-router-dom';
import toast from 'react-hot-toast';
import { ListManagementTab } from '@/components/ui/ListManagementTab/ListManagementTab';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import { Switch } from '@/components/ui/primitives/Switch/Switch';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import {
  useArtifactPagesQuery,
  useDeleteArtifactMutation,
  useUpdateArtifactMutation,
} from '@/hooks/queries/useArtifactQueries';
import type { Artifact } from '@/types/artifact.types';
import { formatRelativeTime } from '@/utils/date';
import { artifactService } from '@/services/artifactService';
import { RenameModal } from '@/components/ui/RenameModal/RenameModal';
import styles from './ArtifactsSettingsTab.module.scss';

export function ArtifactsSettingsTab() {
  const [search, setSearch] = useState('');
  const query = useDebouncedValue(search.trim(), 250);
  const pages = useArtifactPagesQuery(query);
  const deleteArtifact = useDeleteArtifactMutation();
  const updateArtifact = useUpdateArtifactMutation();
  const items = useMemo(() => pages.data?.pages.flat() ?? null, [pages.data]);
  const [renaming, setRenaming] = useState<Artifact | null>(null);

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

  const handleRename = useCallback(
    async (title: string) => {
      if (!renaming) return;
      try {
        await updateArtifact.mutateAsync({ artifactId: renaming.id, data: { title } });
        toast.success('Artifact renamed');
        setRenaming(null);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to rename artifact');
      }
    },
    [renaming, updateArtifact],
  );

  const handlePin = useCallback(
    async (artifact: Artifact) => {
      try {
        await updateArtifact.mutateAsync({
          artifactId: artifact.id,
          data: { pinned: !artifact.pinned_at },
        });
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to pin artifact');
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
    <>
      <ListManagementTab<Artifact>
        title="Artifacts"
        description="Pages agents published for you. Private ones open only for you while signed in; public ones open for anyone with the link."
        items={items}
        emptyIcon={AppWindow}
        emptyText={
          query
            ? 'No artifacts match your search'
            : 'No artifacts yet — ask an agent to build a page'
        }
        toolbar={
          <div className={styles['search-wrap']}>
            <Search className={styles['search-icon']} aria-hidden="true" />
            <Input
              variant="unstyled"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search artifacts…"
              aria-label="Search artifacts"
              className={styles['search-input']}
            />
          </div>
        }
        footerContent={
          pages.hasNextPage && (
            <div className={styles['load-more']}>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => void pages.fetchNextPage()}
                isLoading={pages.isFetchingNextPage}
              >
                Load more
              </Button>
            </div>
          )
        }
        deleteConfirmTitle="Delete artifact"
        deleteConfirmMessage={(artifact) =>
          `Delete "${artifact.title}"? Its link stops working for everyone. This can't be undone.`
        }
        getItemKey={(artifact) => artifact.id}
        onEdit={(index) => items?.[index] && setRenaming(items[index])}
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
              {artifact.pinned_at && <span>Pinned</span>}
              <span>{artifact.is_public ? 'Public' : 'Private'}</span>
              <span>v{artifact.version}</span>
              <span className={styles.muted}>
                Updated {formatRelativeTime(artifact.updated_at)}
              </span>
            </div>
          </>
        )}
        renderItemActions={(artifact) => (
          <>
            <button
              type="button"
              onClick={() => void handlePin(artifact)}
              className={clsx(
                styles['icon-button'],
                artifact.pinned_at && styles['icon-button--active'],
              )}
              aria-label={artifact.pinned_at ? 'Unpin' : 'Pin to top'}
              title={artifact.pinned_at ? 'Unpin' : 'Pin to top'}
            >
              {artifact.pinned_at ? (
                <PinOff className={styles['icon']} />
              ) : (
                <Pin className={styles['icon']} />
              )}
            </button>
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
      <RenameModal
        isOpen={renaming !== null}
        onClose={() => setRenaming(null)}
        currentTitle={renaming?.title ?? ''}
        isLoading={updateArtifact.isPending}
        onSave={handleRename}
        title="Rename artifact"
      />
    </>
  );
}
