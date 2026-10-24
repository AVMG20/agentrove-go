import type { MessageSegment } from '@/components/chat/message-bubble/segmentBuilder';
import { parseAgentRoveResult } from './agentRoveDescriptors';

export interface PublishedArtifact {
  id: string;
  title: string;
  version: number;
}

const CREATE_ARTIFACT = 'mcp__agentrove__create_artifact';

export const extractArtifact = (result: unknown): PublishedArtifact | null => {
  const artifact = parseAgentRoveResult(result).data?.artifact;
  if (typeof artifact !== 'object' || artifact === null) return null;
  const { id, title, version } = artifact as Record<string, unknown>;
  if (typeof id !== 'string' || typeof title !== 'string' || typeof version !== 'number') {
    return null;
  }
  return { id, title, version };
};

// One card per artifact: a turn that publishes and then edits the same page
// shows its latest version, still labelled as published when it began here.
export const latestArtifactSegments = (segments: MessageSegment[]): MessageSegment[] => {
  const idOf = (segment: MessageSegment) =>
    segment.kind === 'tool' ? extractArtifact(segment.tool.result)?.id : undefined;
  const last = new Map<string, number>();
  const created = new Set<string>();
  segments.forEach((segment, i) => {
    const id = idOf(segment);
    if (!id) return;
    last.set(id, i);
    if (segment.kind === 'tool' && segment.tool.name === CREATE_ARTIFACT) created.add(id);
  });
  return segments.flatMap((segment, i) => {
    const id = idOf(segment);
    if (!id) return [segment];
    if (last.get(id) !== i) return [];
    if (segment.kind !== 'tool' || !created.has(id) || segment.tool.name === CREATE_ARTIFACT) {
      return [segment];
    }
    return [{ ...segment, tool: { ...segment.tool, name: CREATE_ARTIFACT } }];
  });
};
