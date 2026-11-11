import { memo, Suspense } from 'react';
import { getToolComponent } from '@/components/chat/tools/registry';
import { ToolCardCompactContext } from '@/components/chat/tools/common/ToolCard/ToolCardCompactContext';
import type { MessageSegment } from './segmentBuilder';

interface StepActionProps {
  segment: MessageSegment;
  isActiveThinking: boolean;
  chatId?: string;
}

// One line of text naming what a step is doing, for a folded group's header.
// A tool reuses its own card title through the compact ToolCard.
export const StepAction = memo(function StepAction({
  segment,
  isActiveThinking,
  chatId,
}: StepActionProps) {
  if (segment.kind === 'thinking') {
    if (isActiveThinking) return 'Thinking';
    return segment.text.trim().split('\n')[0] || 'Thought';
  }
  if (segment.kind !== 'tool') return null;
  const Component = getToolComponent(segment.tool.name);
  return (
    <ToolCardCompactContext value>
      <Suspense fallback={null}>
        <Component tool={segment.tool} chatId={chatId} />
      </Suspense>
    </ToolCardCompactContext>
  );
});
