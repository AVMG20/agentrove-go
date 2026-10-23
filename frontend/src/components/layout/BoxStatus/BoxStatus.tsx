import { useId, useState } from 'react';
import toast from 'react-hot-toast';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { useBoxQuery, useStopBoxMutation } from '@/hooks/queries/useWorkspaceQueries';
import { useDropdown } from '@/hooks/useDropdown';
import { useNow } from '@/hooks/useNow';
import { BOX_HOME, BOX_PROJECTS, formatIdleDuration, summarizeBox } from '@/utils/box';
import { formatBytes } from '@/utils/format';
import styles from './BoxStatus.module.scss';

const RESET_MESSAGE =
  `Stops the box and discards system packages installed with apt, so it starts fresh from the latest image. ` +
  `Your home folder (${BOX_HOME}) with its toolchains, settings and Claude login, and every project in ${BOX_PROJECTS}, are kept.`;

// "● Box running · idles in 12m" in the sidebar footer; click for stop/reset.
// Hidden on host-only setups, where there is no box.
export function BoxStatus() {
  const { data: box, dataUpdatedAt } = useBoxQuery();
  const stopBox = useStopBoxMutation();
  const now = useNow(30_000);
  const { isOpen, setIsOpen, dropdownRef } = useDropdown();
  const [confirmReset, setConfirmReset] = useState(false);
  const panelId = useId();

  if (!box?.available) return null;

  const elapsed = Math.max(0, (now - dataUpdatedAt) / 1000);
  const { tone, label, detail } = summarizeBox(box, elapsed);
  const isRunning = box.state === 'running';

  const stop = async (reset: boolean) => {
    try {
      await stopBox.mutateAsync({ reset });
      toast.success(reset ? 'Box reset. It starts fresh next time.' : 'Box stopped');
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to stop the box');
    }
  };

  return (
    <div ref={dropdownRef} className={styles.status}>
      <button
        type="button"
        className={styles.trigger}
        onClick={() => setIsOpen((open) => !open)}
        aria-expanded={isOpen}
        aria-controls={panelId}
      >
        <span className={clsx(styles.dot, styles[`dot--${tone}`])} aria-hidden="true" />
        <span className={styles.text}>
          {label}
          <span className={styles.detail}>{` · ${detail}`}</span>
        </span>
      </button>

      {isOpen && (
        <div id={panelId} className={styles.panel} role="dialog" aria-label="Your box">
          <div className={styles.heading}>
            <span className={clsx(styles.dot, styles[`dot--${tone}`])} aria-hidden="true" />
            Your box
            <span className={styles.state}>{isRunning ? 'Running' : 'Stopped'}</span>
          </div>
          <p className={styles.description}>
            One Linux box holds all your projects. It starts when you chat
            {box.idle_ttl_seconds > 0 &&
              ` and stops after ${formatIdleDuration(box.idle_ttl_seconds)} without activity`}
            . Everything in <code>{BOX_HOME}</code> stays.
          </p>
          {box.disk_bytes != null && (
            <p className={styles.meta}>System packages: {formatBytes(box.disk_bytes)}</p>
          )}
          {box.busy && (
            <p className={styles.meta}>
              Claude is working in it. You can stop it once the turn ends.
            </p>
          )}
          {!box.busy && isRunning && (
            <p className={styles.meta}>Stopping ends open terminals and background servers.</p>
          )}
          <div className={styles.actions}>
            <Button
              type="button"
              size="sm"
              variant="outline"
              disabled={!isRunning || box.busy}
              isLoading={stopBox.isPending && !stopBox.variables?.reset}
              onClick={() => void stop(false)}
            >
              Stop now
            </Button>
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={box.busy || box.state === 'none'}
              isLoading={stopBox.isPending && !!stopBox.variables?.reset}
              onClick={() => setConfirmReset(true)}
            >
              Reset system…
            </Button>
          </div>
        </div>
      )}

      <ConfirmDialog
        isOpen={confirmReset}
        onClose={() => setConfirmReset(false)}
        onConfirm={() => void stop(true)}
        title="Reset the box system?"
        message={RESET_MESSAGE}
        confirmLabel="Reset system"
      />
    </div>
  );
}
