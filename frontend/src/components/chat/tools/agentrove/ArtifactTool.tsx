import { memo, useEffect, type MouseEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { AppWindow, ArrowUpRight } from 'lucide-react';
import clsx from 'clsx';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { useOpenArtifactId } from '@/hooks/useOpenArtifact';
import { useUIStore } from '@/store/uiStore';
import type { Artifact } from '@/types/artifact.types';
import type { ToolAggregate } from '@/types/tools.types';
import { AgentRoveTool } from './AgentRoveTool';
import { extractArtifact, type PublishedArtifact } from './artifactResult';
import styles from './ArtifactTool.module.scss';

// A modified click means "somewhere else" (new tab/window), like any link.
const isPlainClick = (e: MouseEvent) =>
  e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;

// create/update/edit/restore_artifact: a card that opens the published page
// beside the chat (↗ for its own tab) once the call succeeded; the generic
// Bitbox line while running or on failure.
export const ArtifactTool = memo(function ArtifactTool({
  tool,
  chatId,
}: {
  tool: ToolAggregate;
  chatId?: string;
}) {
  const artifact = tool.status === 'completed' ? extractArtifact(tool.result) : null;
  if (!artifact) return <AgentRoveTool tool={tool} />;
  return <ArtifactCard artifact={artifact} chatId={chatId} published={isCreate(tool)} />;
});

const isCreate = (tool: ToolAggregate) => tool.name.endsWith('create_artifact');

function ArtifactCard({
  artifact,
  chatId,
  published,
}: {
  artifact: PublishedArtifact;
  chatId?: string;
  published: boolean;
}) {
  const href = `/artifacts/${artifact.id}`;
  const openId = useOpenArtifactId(chatId ?? '');
  const isOpen = !!chatId && openId === artifact.id;
  useRefreshOnNewVersion(artifact, chatId);

  const body = (
    <>
      <div className={styles['icon-box']}>
        <AppWindow className={styles.icon} />
      </div>
      <div className={styles.body}>
        <span className={styles.title}>{artifact.title}</span>
        <span className={styles.meta}>
          {published ? 'Published' : 'Updated'} artifact · v{artifact.version}
        </span>
      </div>
    </>
  );

  return (
    <div className={clsx(styles.card, isOpen && styles['card--open'])}>
      {chatId ? (
        // A real link so middle/⌘-click still opens the page on its own.
        <a
          href={href}
          className={styles.main}
          aria-current={isOpen || undefined}
          onClick={(e) => {
            if (!isPlainClick(e)) return;
            e.preventDefault();
            useUIStore.getState().toggleArtifact(chatId, artifact.id);
          }}
        >
          {body}
        </a>
      ) : (
        <div className={styles.main}>{body}</div>
      )}
      <FloatingTooltip content="Open in new tab">
        <a
          href={href}
          target="_blank"
          rel="noopener noreferrer"
          className={styles.open}
          aria-label="Open in new tab"
        >
          <ArrowUpRight className={styles['open-icon']} />
        </a>
      </FloatingTooltip>
    </div>
  );
}

// Artifacts a card already asked about this session: one missing from the list
// (deleted, or another chat's) shouldn't refetch it on every visit.
const refreshedMissing = new Set<string>();

// The panel list and an open viewer refresh when the turn ends; a card for a
// version they don't have yet means the agent just published it, so refresh now
// to show it while the turn is still running.
function useRefreshOnNewVersion(artifact: PublishedArtifact, chatId: string | undefined) {
  const queryClient = useQueryClient();
  const { id, version } = artifact;
  useEffect(() => {
    if (!chatId) return;
    // Not loaded yet: the first fetch will be current anyway.
    const list = queryClient.getQueryData<Artifact[]>(queryKeys.chatArtifacts(chatId));
    const listed = list?.find((a) => a.id === id);
    if (!list || (listed && listed.version >= version)) return;
    if (!listed) {
      if (refreshedMissing.has(id)) return;
      refreshedMissing.add(id);
    }
    void queryClient.invalidateQueries({ queryKey: queryKeys.chatArtifacts(chatId) });
    void queryClient.invalidateQueries({ queryKey: queryKeys.artifactView(id) });
  }, [queryClient, chatId, id, version]);
}
