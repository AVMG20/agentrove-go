import { memo, useMemo, type ReactNode } from 'react';
import { Loader2 } from 'lucide-react';
import clsx from 'clsx';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { useSearchChatsQuery } from '@/hooks/queries/useChatQueries';
import type { ChatSearchResult } from '@/types/chat.types';
import { ChatSearchResultGroup } from './ChatSearchResultGroup';
import styles from './ChatSearchPanel.module.scss';

// Message-content search results for a query typed elsewhere (the command menu's field).
export interface ChatSearchPanelProps {
  query: string;
  onOpenChat: (chatId: string) => void;
  // A section of "All": no typing hint, and no empty notice while other sections matched.
  inline?: boolean;
  hideEmpty?: boolean;
  // Section title shown above the results once there are any.
  heading?: ReactNode;
}

interface WorkspaceGroup {
  workspaceId: string;
  workspaceName: string;
  results: ChatSearchResult[];
}

function groupByWorkspace(results: ChatSearchResult[]): WorkspaceGroup[] {
  const groups = new Map<string, WorkspaceGroup>();
  for (const result of results) {
    let group = groups.get(result.workspace_id);
    if (!group) {
      group = {
        workspaceId: result.workspace_id,
        workspaceName: result.workspace_name,
        results: [],
      };
      groups.set(result.workspace_id, group);
    }
    group.results.push(result);
  }
  return Array.from(groups.values());
}

export const ChatSearchPanel = memo(function ChatSearchPanel({
  query,
  onOpenChat,
  inline = false,
  hideEmpty = false,
  heading,
}: ChatSearchPanelProps) {
  const debouncedQuery = useDebouncedValue(query, 250);

  const { data, isFetching, error } = useSearchChatsQuery(debouncedQuery);
  const hasLoaded = !!data;
  const truncated = !!data?.truncated;

  const { grouped, totalMatches, resultCount } = useMemo(() => {
    const results = data?.results ?? [];
    return {
      grouped: groupByWorkspace(results),
      totalMatches: results.reduce((acc, r) => acc + r.match_count, 0),
      resultCount: results.length,
    };
  }, [data]);

  const hasQuery = debouncedQuery.trim().length >= 2;
  const hasResults = resultCount > 0;

  return (
    <div
      className={clsx(styles['chat-search-panel'], inline && styles['chat-search-panel--inline'])}
    >
      <div className={styles.results}>
        {!hasQuery && !inline && (
          <p className={styles['empty-message']}>Type at least 2 characters to search.</p>
        )}

        {/* Keep the spinner until something is showable. */}
        {hasQuery && isFetching && !hasResults && (
          <div className={styles['loading-message']}>
            <Loader2 className={styles['spin-icon']} />
            Searching...
          </div>
        )}

        {/* Blocking failure means nothing loaded and nothing is still in flight.
            Once results loaded, a failed refetch degrades to a non-blocking
            incompleteness notice, coherent alongside results or "No results". */}
        {hasQuery && error && !hasLoaded && !isFetching && (
          <p className={styles['error-message']}>
            {error instanceof Error ? error.message : 'Search failed'}
          </p>
        )}

        {hasQuery && error && hasLoaded && (
          <p className={styles['error-message']}>Search failed — results may be incomplete.</p>
        )}

        {hasQuery && hasLoaded && !hasResults && !isFetching && !hideEmpty && (
          <p className={styles['empty-message']}>No results for &ldquo;{debouncedQuery}&rdquo;</p>
        )}

        {hasQuery && hasResults && (
          <>
            {heading}
            <p className={styles.summary}>
              {isFetching && <Loader2 className={styles['spin-icon']} />}
              <span>
                {totalMatches} {totalMatches === 1 ? 'result' : 'results'} in {resultCount}{' '}
                {resultCount === 1 ? 'chat' : 'chats'} · {grouped.length}{' '}
                {grouped.length === 1 ? 'project' : 'projects'}
                {truncated && ' (truncated)'}
              </span>
            </p>
            <div aria-busy={isFetching} className={styles['result-list']}>
              {grouped.map((group) => (
                <div key={group.workspaceId} className={styles['workspace-group']}>
                  <p className={styles['workspace-name']}>{group.workspaceName}</p>
                  {group.results.map((result) => (
                    <ChatSearchResultGroup
                      key={result.chat_id}
                      result={result}
                      onOpen={onOpenChat}
                    />
                  ))}
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
});
