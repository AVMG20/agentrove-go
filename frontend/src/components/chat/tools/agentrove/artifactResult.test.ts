import { describe, expect, it } from 'vitest';
import type { MessageSegment } from '@/components/chat/message-bubble/segmentBuilder';
import { latestArtifactSegments } from './artifactResult';

const artifactCall = (
  segId: string,
  name: string,
  artifact: { id: string; title: string; version: number },
): MessageSegment => ({
  kind: 'tool',
  id: segId,
  tool: {
    id: segId,
    name: `mcp__agentrove__${name}`,
    title: name,
    status: 'completed',
    result: [{ type: 'text', text: JSON.stringify({ artifact }) }],
    children: [],
  },
});

const names = (segments: MessageSegment[]) =>
  segments.map((s) => (s.kind === 'tool' ? `${s.id}:${s.tool.name.split('__')[2]}` : s.id));

describe('latestArtifactSegments', () => {
  it('keeps one card per artifact, the latest version, still labelled as published', () => {
    const page = { id: 'a1', title: 'How it works', version: 1 };
    const out = latestArtifactSegments([
      artifactCall('s1', 'create_artifact', page),
      artifactCall('s2', 'edit_artifact', { ...page, version: 2 }),
    ]);
    expect(names(out)).toEqual(['s2:create_artifact']);
  });

  it('keeps an update of an artifact made in an earlier turn as an update', () => {
    const out = latestArtifactSegments([
      artifactCall('s1', 'edit_artifact', { id: 'a1', title: 'Old', version: 3 }),
      artifactCall('s2', 'edit_artifact', { id: 'a1', title: 'Old', version: 4 }),
    ]);
    expect(names(out)).toEqual(['s2:edit_artifact']);
  });

  it('keeps separate artifacts apart, in order', () => {
    const out = latestArtifactSegments([
      artifactCall('s1', 'create_artifact', { id: 'a1', title: 'One', version: 1 }),
      artifactCall('s2', 'create_artifact', { id: 'a2', title: 'Two', version: 1 }),
      artifactCall('s3', 'update_artifact', { id: 'a1', title: 'One', version: 2 }),
    ]);
    expect(names(out)).toEqual(['s2:create_artifact', 's3:create_artifact']);
  });
});
