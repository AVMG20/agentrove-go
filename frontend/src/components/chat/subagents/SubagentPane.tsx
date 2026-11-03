import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import clsx from 'clsx';
import { ArrowLeft, Square, X } from 'lucide-react';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import { MarkDown } from '@/components/ui/markdown/MarkDown';
import { statusIndicator } from '@/components/chat/tools/common/statusIndicator';
import { useChatSubagentsQuery, useSubagentQuery } from '@/hooks/queries/useSubagentQueries';
import { useIsMobile } from '@/hooks/useIsMobile';
import { useSecondTick } from '@/hooks/useSecondTick';
import { chatService } from '@/services/chatService';
import { useUIStore } from '@/store/uiStore';
import type { Subagent, SubagentActivity } from '@/types/chat.types';
import type { TileId } from '@/types/ui.types';
import { subagentDuration, subagentStatusLabel } from '@/utils/subagents';
import { SubagentStatusIcon } from './SubagentRow';
import styles from './SubagentPane.module.scss';

interface SubagentPaneProps {
  chatId: string;
  tileId: TileId;
  isVisible: boolean;
}

// One subagent next to the chat: what it was asked, what it's doing, what it
// came back with. It's always opened on a specific subagent (from the tray above
// the composer, a tool card or the session panel); a restored layout without one
// falls back to the running subagent, or closes.
export function SubagentPane({ chatId, tileId, isVisible }: SubagentPaneProps) {
  const subagentId = useUIStore((s) => s.subagentByChat[chatId]);
  const close = () => useUIStore.getState().removeTab(tileId);
  if (!subagentId) return <SubagentFallback chatId={chatId} onClose={close} />;
  // Keyed so switching subagents starts fresh: no stop spinner, expanded
  // prompt or scroll-follow state carried over from the previous one.
  return (
    <SubagentDetail
      key={subagentId}
      chatId={chatId}
      subagentId={subagentId}
      isVisible={isVisible}
      onClose={close}
    />
  );
}

function SubagentFallback({ chatId, onClose }: { chatId: string; onClose: () => void }) {
  const { data: subagents, isLoading } = useChatSubagentsQuery(chatId);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    if (isLoading) return;
    const running = subagents?.find((s) => s.status === 'running');
    if (running) {
      useUIStore.setState((s) => ({
        subagentByChat: { ...s.subagentByChat, [chatId]: running.id },
      }));
    } else {
      onCloseRef.current();
    }
  }, [chatId, isLoading, subagents]);
  return <section className={styles.pane} aria-label="Subagent" aria-busy />;
}

function CloseButton({ onClose }: { onClose: () => void }) {
  return (
    <FloatingTooltip content="Close">
      <Button
        variant="unstyled"
        type="button"
        className={styles['icon-button']}
        onClick={onClose}
        aria-label="Close subagent view"
      >
        <X className={styles.glyph} />
      </Button>
    </FloatingTooltip>
  );
}

function SubagentDetail({
  chatId,
  subagentId,
  isVisible,
  onClose,
}: {
  chatId: string;
  subagentId: string;
  isVisible: boolean;
  onClose: () => void;
}) {
  const { data: list } = useChatSubagentsQuery(chatId);
  const { data: detail, isError } = useSubagentQuery(chatId, subagentId, isVisible);
  // The list follows chat events; prefer it for the header so it never lags.
  const summary = list?.find((s) => s.id === subagentId);
  const subagent: Subagent | undefined = summary ? { ...detail, ...summary } : detail;
  const running = subagent?.status === 'running';
  const now = useSecondTick(running && isVisible);
  const bodyRef = useRef<HTMLDivElement>(null);
  const isMobile = useIsMobile();
  const stoppable = !!(running && subagent?.background && subagent.task_id);

  return (
    <section className={styles.pane} aria-label="Subagent">
      <header className={styles.header}>
        {isMobile && (
          <Button
            variant="unstyled"
            type="button"
            className={styles['back-button']}
            onClick={onClose}
            aria-label="Back to chat"
          >
            <ArrowLeft className={styles['back-glyph']} />
          </Button>
        )}
        <div className={styles['header-text']}>
          <h2 className={styles.title}>
            {subagent && <SubagentStatusIcon status={subagent.status} />}
            <span className={styles['title-text']}>
              {subagent?.description || subagent?.subagent_type || 'Subagent'}
            </span>
          </h2>
          {subagent && (
            <p className={styles.meta}>
              {[
                subagent.subagent_type,
                subagentStatusLabel(subagent),
                subagentDuration(subagent, now),
                `${subagent.tool_count} ${subagent.tool_count === 1 ? 'tool' : 'tools'}`,
              ].join(' · ')}
            </p>
          )}
        </div>
        {!isMobile && stoppable && <StopButton chatId={chatId} taskId={subagent.task_id!} />}
        {!isMobile && <CloseButton onClose={onClose} />}
      </header>
      <div ref={bodyRef} className={styles.body}>
        {!subagent && !isError && <p className={styles.empty}>Loading…</p>}
        {!subagent && isError && (
          <p className={styles.empty}>This subagent is no longer available.</p>
        )}
        {detail?.prompt && <Prompt text={detail.prompt} />}
        {detail && (
          <Activity
            entries={detail.activity ?? []}
            running={running}
            isVisible={isVisible}
            scrollerRef={bodyRef}
          />
        )}
        {subagent?.result && subagent.status !== 'running' && (
          <section className={styles.section}>
            <h3 className={styles['section-title']}>Result</h3>
            <div className={styles.result}>
              <MarkDown content={subagent.result} />
            </div>
          </section>
        )}
      </div>
      {isMobile && stoppable && (
        <footer className={styles.footer}>
          <StopButton chatId={chatId} taskId={subagent!.task_id!} wide />
        </footer>
      )}
    </section>
  );
}

function StopButton({
  chatId,
  taskId,
  wide = false,
}: {
  chatId: string;
  taskId: string;
  // The phone's full-width bar under the activity, with a label.
  wide?: boolean;
}) {
  const [stopping, setStopping] = useState(false);
  const stop = async () => {
    setStopping(true);
    try {
      await chatService.stopBackgroundTask(chatId, taskId);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to stop the subagent');
      setStopping(false);
    }
  };
  if (wide) {
    return (
      <Button
        variant="unstyled"
        type="button"
        className={styles['stop-bar']}
        onClick={() => void stop()}
        disabled={stopping}
        aria-label="Stop subagent"
      >
        {stopping ? <Spinner size="xs" /> : <Square className={styles['stop-glyph']} />}
        {stopping ? 'Stopping…' : 'Stop subagent'}
      </Button>
    );
  }
  return (
    <FloatingTooltip content="Stop subagent">
      <Button
        variant="unstyled"
        type="button"
        className={styles['icon-button']}
        onClick={() => void stop()}
        disabled={stopping}
        aria-label="Stop subagent"
      >
        {stopping ? <Spinner size="xs" /> : <Square className={styles['stop-glyph']} />}
      </Button>
    </FloatingTooltip>
  );
}

// Long prompts scroll in a capped box; Expand lifts the cap.
function Prompt({ text }: { text: string }) {
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const ref = useRef<HTMLParagraphElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (el && !expanded) setOverflows(el.scrollHeight > el.clientHeight + 1);
  }, [text, expanded]);
  return (
    <section className={styles.section}>
      <h3 className={styles['section-title']}>Prompt</h3>
      <p ref={ref} className={clsx(styles.prompt, !expanded && styles['prompt--clamped'])}>
        {text}
      </p>
      {(overflows || expanded) && (
        <Button
          variant="unstyled"
          type="button"
          className={styles['more-button']}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? 'Collapse' : 'Expand'}
        </Button>
      )}
    </section>
  );
}

function Activity({
  entries,
  running,
  isVisible,
  scrollerRef,
}: {
  entries: SubagentActivity[];
  running: boolean;
  isVisible: boolean;
  scrollerRef: RefObject<HTMLDivElement | null>;
}) {
  const followRef = useRef(true);
  // Follow new activity unless the reader scrolled up to look at older steps.
  useEffect(() => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    const onScroll = () => {
      followRef.current = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 48;
    };
    scroller.addEventListener('scroll', onScroll, { passive: true });
    return () => scroller.removeEventListener('scroll', onScroll);
  }, [scrollerRef]);
  // Keyed on the array, not its length: the log is capped, so the length stops
  // changing while new entries still arrive.
  useEffect(() => {
    const scroller = scrollerRef.current;
    if (scroller && isVisible && running && followRef.current) {
      scroller.scrollTop = scroller.scrollHeight;
    }
  }, [entries, running, isVisible, scrollerRef]);

  return (
    <section className={styles.section}>
      <h3 className={styles['section-title']}>Activity</h3>
      {entries.length === 0 && !running && <p className={styles.empty}>No activity recorded.</p>}
      <ol className={styles.activity}>
        {entries.map((entry, i) =>
          entry.kind === 'tool' ? (
            <li key={entry.id ?? i} className={styles.tool}>
              <span className={styles['tool-icon']}>
                {statusIndicator[entry.status ?? 'started']}
              </span>
              <span className={styles['tool-title']}>{entry.title || entry.name}</span>
            </li>
          ) : (
            <li key={i} className={styles.text}>
              {entry.text}
            </li>
          ),
        )}
        {running && <li className={styles.working}>Working…</li>}
      </ol>
    </section>
  );
}
