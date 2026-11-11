import { useState } from 'react';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { useBoxQuery, usePruneBoxMutation } from '@/hooks/queries/useWorkspaceQueries';
import { BOX_HOME, boxSizeBreakdown, pruneSummary } from '@/utils/box';
import { formatBytes } from '@/utils/format';
import styles from './GeneralSettingsTab.module.scss';

const PRUNE_MESSAGE =
  `Frees disk space in your box. Removed: system packages installed with apt, toolchains and caches in ` +
  `${BOX_HOME} (Rust, Go, npm, pip…), and the files of projects cloned from git. ` +
  `Kept: your threads, projects, Claude and other logins, git and SSH settings, and any project with ` +
  `uncommitted, unpushed or stashed work. A pruned project is cloned again the next time you use it; ` +
  `reinstall toolchains when you need them.`;

// Data management row: the box's size and a button to prune it.
export function PruneBoxRow() {
  const { data: box } = useBoxQuery();
  const prune = usePruneBoxMutation();
  const [confirming, setConfirming] = useState(false);

  if (!box?.available) return null;

  const run = async () => {
    const id = toast.loading('Pruning your box…');
    try {
      const result = await prune.mutateAsync();
      toast.success(pruneSummary(result), { id, duration: 8000 });
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to prune the box', { id });
    }
  };

  const breakdown = boxSizeBreakdown(box);
  return (
    <div className={styles['data-row']}>
      <div className={styles['pref-text']}>
        <h3 className={styles['setting-title']}>
          Prune Box{box.total_bytes != null && ` · ${formatBytes(box.total_bytes)}`}
        </h3>
        <p className={styles['field-desc']}>
          Remove installed packages, toolchains and project files to free space. Threads and
          projects stay; projects are cloned again when you use them.
          {breakdown && ` ${breakdown}.`}
        </p>
      </div>
      <Button
        type="button"
        onClick={() => setConfirming(true)}
        variant="outline"
        size="sm"
        disabled={box.busy}
        isLoading={prune.isPending}
        className={styles['delete-button']}
      >
        Prune…
      </Button>
      <ConfirmDialog
        isOpen={confirming}
        onClose={() => setConfirming(false)}
        onConfirm={() => void run()}
        title="Prune your box?"
        message={PRUNE_MESSAGE}
        confirmLabel="Prune box"
      />
    </div>
  );
}
