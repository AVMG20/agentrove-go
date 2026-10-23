import { logger } from '@/utils/logger';
import { PAGE_RESUME_IDLE_MS, isOffline, subscribePageResume } from '@/utils/pageResume';

const REOPEN_DELAY_MS = 15_000;

// Chat lifecycle SSE: EventSource retries transients but treats HTTP errors
// (e.g. expired query-param token) as fatal CLOSED — reopen via createSource.
// No server-side replay, so successful open after any failure calls onResync.
export function subscribeChatEventsFeed(options: {
  createSource: () => Promise<EventSource>;
  onChatEvent: (event: MessageEvent) => void;
  onResync: () => void;
  logContext: string;
}): () => void {
  const { createSource, onChatEvent, onResync, logContext } = options;
  let source: EventSource | undefined;
  let cancelled = false;
  let retryTimer: number | undefined;
  let needsResync = false;
  // createSource() in flight — a resume must not race it with a second socket.
  let opening = false;

  const scheduleReopen = () => {
    if (cancelled) return;
    retryTimer = window.setTimeout(openFeed, REOPEN_DELAY_MS);
  };

  const openFeed = () => {
    opening = true;
    createSource()
      .then((eventSource) => {
        opening = false;
        if (cancelled) {
          eventSource.close();
          return;
        }
        source = eventSource;
        // Custom event names hit the generic EventTarget overload, which takes Event.
        source.addEventListener('chat_event', (event: Event) => onChatEvent(event as MessageEvent));
        source.onopen = () => {
          if (needsResync) {
            needsResync = false;
            onResync();
          }
        };
        source.onerror = () => {
          // Gaps include transient drops retried natively (never hit CLOSED).
          needsResync = true;
          if (source?.readyState !== EventSource.CLOSED) return;
          scheduleReopen();
        };
      })
      .catch((error: unknown) => {
        opening = false;
        logger.error('Chat events stream failed to open', logContext, error);
        needsResync = true;
        scheduleReopen();
      });
  };

  // Comment pings are invisible to EventSource, so a half-open socket after sleep
  // looks OPEN — after a long away replace it outright; resync covers missed events.
  const unsubscribeResume = subscribePageResume(({ idleMs }) => {
    if (cancelled || opening || isOffline()) return;
    const open = source?.readyState === EventSource.OPEN;
    if (open && idleMs < PAGE_RESUME_IDLE_MS) return;
    window.clearTimeout(retryTimer);
    retryTimer = undefined;
    source?.close();
    source = undefined;
    needsResync = true;
    openFeed();
  });

  openFeed();

  return () => {
    cancelled = true;
    unsubscribeResume();
    window.clearTimeout(retryTimer);
    source?.close();
  };
}
