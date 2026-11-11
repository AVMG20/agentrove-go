import { useState } from 'react';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { useBoxQuery, usePruneBoxMutation } from '@/hooks/queries/useWorkspaceQueries';
import { BoxChanges } from '@/components/layout/BoxStatus/BoxChanges';
import { BoxStorage } from '@/components/layout/BoxStatus/BoxStorage';
import { pruneSummary } from '@/utils/box';
import styles from './GeneralSettingsTab.module.scss';

const PRUNE_MESSAGE = (
  <BoxChanges
    intro="Resets your box to nearly clean to free disk space. Projects are cloned again when you next use them; reinstall toolchains when you need them."
    removes={[
      'System packages installed with apt',
      'Toolchains, caches and global installs in ~ (Rust, Go, npm, pip…)',
      'The files of every project cloned from git, including uncommitted, unpushed and stashed work',
    ]}
    keeps={[
      'Configs and logins: Claude, git, SSH and your shell',
      'Your threads and projects',
      'Projects not cloned from git',
    ]}
  />
);

// Data management row: the box's storage and a button to prune it.
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

  return (
    <div className={styles['data-row']}>
      <div className={styles['pref-text']}>
        <h3 className={styles['setting-title']}>Prune Box</h3>
        <p className={styles['field-desc']}>
          Reset your box to nearly clean: installed packages, toolchains and project files go;
          configs, logins and threads stay.
        </p>
        <BoxStorage box={box} className={styles['box-storage']} />
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
