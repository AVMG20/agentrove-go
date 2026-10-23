import { memo } from 'react';
import { AppWindow, ArrowUpRight } from 'lucide-react';
import type { ToolAggregate } from '@/types/tools.types';
import { AgentRoveTool } from './AgentRoveTool';
import { parseAgentRoveResult } from './agentRoveDescriptors';
import styles from './ArtifactTool.module.scss';

interface PublishedArtifact {
  id: string;
  title: string;
  version: number;
}

const extractArtifact = (result: unknown): PublishedArtifact | null => {
  const artifact = parseAgentRoveResult(result).data?.artifact;
  if (typeof artifact !== 'object' || artifact === null) return null;
  const { id, title, version } = artifact as Record<string, unknown>;
  if (typeof id !== 'string' || typeof title !== 'string' || typeof version !== 'number') {
    return null;
  }
  return { id, title, version };
};

// create/update/edit/restore_artifact: a card linking to the published page once
// the call succeeded; the generic AgentRove line while running or on failure.
export const ArtifactTool = memo(function ArtifactTool({ tool }: { tool: ToolAggregate }) {
  const artifact = tool.status === 'completed' ? extractArtifact(tool.result) : null;
  if (!artifact) return <AgentRoveTool tool={tool} />;

  const href = `/artifacts/${artifact.id}`;
  const verb = tool.name.endsWith('create_artifact') ? 'Published' : 'Updated';

  return (
    <div className={styles.card}>
      <div className={styles['icon-box']}>
        <AppWindow className={styles.icon} />
      </div>
      <div className={styles.body}>
        <span className={styles.title}>{artifact.title}</span>
        <span className={styles.meta}>
          {verb} artifact · v{artifact.version}
        </span>
      </div>
      {/* The viewer is a standalone page — keep the chat open next to it. */}
      <a href={href} target="_blank" rel="noopener noreferrer" className={styles.open}>
        Open
        <ArrowUpRight className={styles['open-icon']} />
      </a>
    </div>
  );
});
