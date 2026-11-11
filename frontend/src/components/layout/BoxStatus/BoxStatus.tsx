import { useId, useState } from 'react';
import toast from 'react-hot-toast';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { useBoxQuery, useStopBoxMutation } from '@/hooks/queries/useWorkspaceQueries';
import { useDropdown } from '@/hooks/useDropdown';
import { useNow } from '@/hooks/useNow';
import { formatIdleDuration, summarizeBox } from '@/utils/box';
import { BoxChanges } from './BoxChanges';
import { BoxStorage } from './BoxStorage';
import styles from './BoxStatus.module.scss';

const RESET_MESSAGE = (
  <BoxChanges
    intro="Stops the box. It starts fresh from the latest image next time."
    removes={['System packages installed with apt']}
    keeps={['Your home folder (~): toolchains, settings and logins', 'All projects and threads']}
  />
);

// "● Box running" in the sidebar footer; click for the idle countdown and stop/reset.
export function BoxStatus() {
  const { data: box, dataUpdatedAt } = useBoxQuery();
  const stopBox = useStopBoxMutation();
  const now = useNow(30_000);
  const { isOpen, setIsOpen, dropdownRef } = useDropdown();
  const [confirmReset, setConfirmReset] = useState(false);
  const panelId = useId();

  if (!box) return null;
  // Every project runs in the box, so a server without Docker can't run any.
  if (!box.available) {
    return (
      <div className={styles.status}>
        <div className={clsx(styles.trigger, styles['trigger--static'])} role="status">
          <span className={clsx(styles.dot, styles['dot--unavailable'])} aria-hidden="true" />
          <span className={styles.text}>
            Docker unavailable
            <span className={styles.detail}> · projects can't run</span>
          </span>
        </div>
      </div>
    );
  }

  const elapsed = Math.max(0, (now - dataUpdatedAt) / 1000);
  const { tone, label, detail } = summarizeBox(box, elapsed);
  const isRunning = box.state === 'running';
  // The countdown ticks; it lives in the panel so the footer stays still.
  const counting = isRunning && !box.busy && box.idle_ttl_seconds > 0;

  const state = isRunning ? `Running · ${detail}` : 'Stopped';
  const note = box.busy
    ? 'Claude is working in it. You can stop it once the turn ends.'
    : isRunning
      ? 'Stopping ends open terminals and background servers.'
      : null;

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
          {!counting && <span className={styles.detail}>{` · ${detail}`}</span>}
        </span>
      </button>

      {isOpen && (
        <div id={panelId} className={styles.panel} role="dialog" aria-label="Your box">
          <div className={styles.heading}>
            <span className={clsx(styles.dot, styles[`dot--${tone}`])} aria-hidden="true" />
            Your box
            <span className={styles.state}>{state}</span>
          </div>
          <p className={styles.description}>
            One Linux box runs all your projects. It starts when you chat
            {box.idle_ttl_seconds > 0 &&
              ` and stops after ${formatIdleDuration(box.idle_ttl_seconds)} idle`}
            .
          </p>
          <BoxStorage box={box} className={styles.storage} />
          <div className={styles.actions}>
            {note && <p className={styles.meta}>{note}</p>}
            <div className={styles.buttons}>
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
