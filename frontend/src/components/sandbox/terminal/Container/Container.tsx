import { Suspense, useState, useCallback, useEffect, useRef } from 'react';
import clsx from 'clsx';
import { Globe, Plus, X } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { TerminalTab } from '@/components/sandbox/terminal/TerminalTab/TerminalTab';
import { viewLoadingFallback } from '@/components/ui/shared/ViewLoadingFallback/ViewLoadingFallback';
import { lazyNamed } from '@/utils/lazyNamed';
import { terminalStorageKey } from '@/utils/terminal';
import styles from './Container.module.scss';

// Loaded on first open: most sessions never look at the browser.
const BrowserTab = lazyNamed(
  () => import('@/components/sandbox/browser/BrowserTab/BrowserTab'),
  'BrowserTab',
);

export interface ContainerProps {
  sandboxId?: string;
  /** Persist key (chat id or landing scope). Without it tabs reset every mount. */
  storageScope?: string;
  worktreeCwd?: string;
  isVisible: boolean;
  panelKey: string;
}

interface TerminalInstance {
  id: string;
  label: string;
}

function readStoredTerminals(
  storageKey: string | null,
  defaultTerminalId: string,
): { terminals: TerminalInstance[]; activeTerminalId: string; browserActive: boolean } {
  const defaults = {
    terminals: [{ id: defaultTerminalId, label: 'Terminal 1' }],
    activeTerminalId: defaultTerminalId,
    browserActive: false,
  };
  if (!storageKey) return defaults;
  const stored = localStorage.getItem(storageKey);
  if (!stored) return defaults;
  try {
    const parsed = JSON.parse(stored) as {
      terminals?: TerminalInstance[];
      activeTerminalId?: string;
      browserActive?: boolean;
    };
    const valid = parsed.terminals?.filter((terminal) => terminal.id && terminal.label) ?? [];
    if (valid.length === 0) return defaults;
    return {
      terminals: valid,
      activeTerminalId:
        parsed.activeTerminalId && valid.some((terminal) => terminal.id === parsed.activeTerminalId)
          ? parsed.activeTerminalId
          : (valid[0]?.id ?? defaultTerminalId),
      browserActive: parsed.browserActive === true,
    };
  } catch {
    // corrupt storage, use defaults
    return defaults;
  }
}

export function Container({
  sandboxId,
  storageScope,
  worktreeCwd,
  isVisible,
  panelKey,
}: ContainerProps) {
  const defaultTerminalId = `terminal-${panelKey}-1`;
  const storageKey = storageScope ? terminalStorageKey(storageScope, panelKey) : null;
  const [terminals, setTerminals] = useState<TerminalInstance[]>(
    () => readStoredTerminals(storageKey, defaultTerminalId).terminals,
  );
  const [activeTerminalId, setActiveTerminalId] = useState<string>(
    () => readStoredTerminals(storageKey, defaultTerminalId).activeTerminalId,
  );
  const [closingTerminalIds, setClosingTerminalIds] = useState<Set<string>>(() => new Set());
  const [browserActive, setBrowserActive] = useState(
    () => readStoredTerminals(storageKey, defaultTerminalId).browserActive,
  );
  // Mounted once opened, then kept (hidden) so switching back is instant;
  // it only holds a connection while visible.
  const [browserMounted, setBrowserMounted] = useState(browserActive);
  const showBrowser = browserActive && Boolean(sandboxId);

  // Restore during render on identity change — effects run too late (would persist old state).
  const restoreKey = `${storageKey}|${defaultTerminalId}`;
  const prevRestoreKeyRef = useRef(restoreKey);
  if (prevRestoreKeyRef.current !== restoreKey) {
    prevRestoreKeyRef.current = restoreKey;
    const restored = readStoredTerminals(storageKey, defaultTerminalId);
    setTerminals(restored.terminals);
    setActiveTerminalId(restored.activeTerminalId);
    setBrowserActive(restored.browserActive);
    if (restored.browserActive) setBrowserMounted(true);
  }

  useEffect(() => {
    if (!storageKey) {
      return;
    }
    localStorage.setItem(
      storageKey,
      JSON.stringify({ terminals, activeTerminalId, browserActive }),
    );
  }, [storageKey, terminals, activeTerminalId, browserActive]);

  const openBrowser = useCallback(() => {
    setBrowserActive(true);
    setBrowserMounted(true);
  }, []);

  const addTerminal = useCallback(() => {
    setTerminals((prev) => {
      const existingNumbers = new Set(prev.map((t) => parseInt(t.id.split('-').pop() || '0', 10)));

      let nextNumber = 1;
      while (existingNumbers.has(nextNumber)) {
        nextNumber += 1;
      }

      const newTerminal: TerminalInstance = {
        id: `terminal-${panelKey}-${nextNumber}`,
        label: `Terminal ${prev.length + 1}`,
      };

      setActiveTerminalId(newTerminal.id);
      setBrowserActive(false);
      return [...prev, newTerminal];
    });
  }, [panelKey]);

  const closeTerminal = useCallback((terminalId: string) => {
    setClosingTerminalIds((prev) => {
      const next = new Set(prev);
      next.add(terminalId);
      return next;
    });
  }, []);

  const finalizeCloseTerminal = useCallback(
    (terminalId: string) => {
      setTerminals((prev) => {
        const filtered = prev.filter((t) => t.id !== terminalId);
        if (filtered.length === 0) {
          setActiveTerminalId(defaultTerminalId);
          return [{ id: defaultTerminalId, label: 'Terminal 1' }];
        }

        setActiveTerminalId((current) => {
          if (current === terminalId) {
            const currentIndex = prev.findIndex((t) => t.id === terminalId);
            const nextTerminal = prev[currentIndex - 1] || prev[currentIndex + 1];
            return nextTerminal?.id || filtered[0]?.id || defaultTerminalId;
          }
          return current;
        });

        return filtered.map((t, i) => ({ ...t, label: `Terminal ${i + 1}` }));
      });
      setClosingTerminalIds((prev) => {
        const next = new Set(prev);
        next.delete(terminalId);
        return next;
      });
    },
    [defaultTerminalId],
  );

  return (
    <div className={styles['terminal-container']}>
      <div className={styles.tablist} role="tablist">
        {terminals.map((terminal) => (
          <Button
            variant="unstyled"
            key={terminal.id}
            className={styles.tab}
            onClick={() => {
              setActiveTerminalId(terminal.id);
              setBrowserActive(false);
            }}
            role="tab"
            aria-selected={!showBrowser && activeTerminalId === terminal.id}
          >
            <span>{terminal.label}</span>
            {terminals.length > 1 && (
              <span
                className={styles['tab-close']}
                onClick={(e) => {
                  e.stopPropagation();
                  closeTerminal(terminal.id);
                }}
                role="button"
                aria-label="Close terminal"
              >
                <X className={styles['tab-close-icon']} />
              </span>
            )}
          </Button>
        ))}
        <Button
          variant="unstyled"
          className={styles['tab-add']}
          onClick={addTerminal}
          aria-label="Add new terminal"
        >
          <Plus className={styles['tab-add-icon']} />
        </Button>
        {sandboxId && (
          <Button
            variant="unstyled"
            className={clsx(styles.tab, styles['tab-browser'])}
            onClick={openBrowser}
            role="tab"
            aria-selected={showBrowser}
          >
            <Globe className={styles['tab-add-icon']} />
            <span>Browser</span>
          </Button>
        )}
      </div>

      <div className={styles.panels}>
        {terminals.map((terminal) => (
          <div
            key={terminal.id}
            className={clsx(
              styles.panel,
              (showBrowser || activeTerminalId !== terminal.id) && styles['panel--hidden'],
            )}
          >
            <TerminalTab
              isVisible={isVisible && !showBrowser && activeTerminalId === terminal.id}
              sandboxId={sandboxId}
              terminalId={terminal.id}
              cwd={worktreeCwd}
              shouldClose={closingTerminalIds.has(terminal.id)}
              onClosed={() => finalizeCloseTerminal(terminal.id)}
            />
          </div>
        ))}
        {browserMounted && sandboxId && (
          <div className={clsx(styles.panel, !showBrowser && styles['panel--hidden'])}>
            <Suspense fallback={viewLoadingFallback}>
              <BrowserTab isVisible={isVisible && showBrowser} sandboxId={sandboxId} />
            </Suspense>
          </div>
        )}
      </div>
    </div>
  );
}
