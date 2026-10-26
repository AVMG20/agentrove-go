import { memo, Suspense } from 'react';
import { MarkDown } from '@/components/ui/markdown/MarkDown';
import { useSmoothText } from '@/hooks/useSmoothText';
import { ThinkingBlock } from './ThinkingBlock';
import { PlanCard } from './PlanCard';
import { MessageAttachments } from './MessageAttachments';
import { UserBubble } from './Message';
import { getToolComponent } from '@/components/chat/tools/registry';
import type { MessageSegment } from './segmentBuilder';
import { Spinner } from '@/components/ui/primitives/Spinner/Spinner';
import styles from './SegmentView.module.scss';

interface SegmentViewProps {
  segment: MessageSegment;
  chatId?: string;
  isActiveThinking: boolean;
  isActiveText: boolean;
  highlightMentions?: boolean;
}

function TextSegment({
  text,
  isActive,
  highlightMentions,
}: {
  text: string;
  isActive: boolean;
  highlightMentions?: boolean;
}) {
  // The segment receiving stream output reveals its text word-by-word instead
  // of jumping a flush-sized chunk at a time.
  const [smoothText] = useSmoothText(text, isActive);
  return (
    <div className={styles['text-segment']}>
      <MarkDown content={smoothText} streaming={isActive} highlightMentions={highlightMentions} />
    </div>
  );
}

function ToolLoadingFallback() {
  return (
    <div className={styles['tool-fallback']}>
      <Spinner size="sm" className={styles['tool-fallback-spinner']} />
      <span className={styles['tool-fallback-text']}>Loading tool output...</span>
    </div>
  );
}

export const SegmentView = memo(function SegmentView({
  segment,
  chatId,
  isActiveThinking,
  isActiveText,
  highlightMentions,
}: SegmentViewProps) {
  // Memoized so stream flushes only re-render segments whose object identity
  // changed — MessageRenderer keeps unchanged segments referentially stable.
  switch (segment.kind) {
    case 'text':
      return (
        <TextSegment
          text={segment.text}
          isActive={isActiveText}
          highlightMentions={highlightMentions}
        />
      );
    case 'steering':
      return (
        <div className={styles['steering-segment']}>
          <UserBubble>
            <MessageAttachments attachments={segment.attachments} />
            <MarkDown content={segment.text} highlightMentions />
          </UserBubble>
        </div>
      );
    case 'thinking':
      return (
        <div className={styles['thinking-segment']}>
          <ThinkingBlock content={segment.text} isActiveThinking={isActiveThinking} />
        </div>
      );
    case 'tool': {
      const Component = getToolComponent(segment.tool.name);
      return (
        <div className={styles['tool-segment']}>
          <Suspense fallback={<ToolLoadingFallback />}>
            <Component tool={segment.tool} chatId={chatId} />
          </Suspense>
        </div>
      );
    }
    case 'plan':
      return (
        <div className={styles['tool-segment']}>
          <PlanCard entries={segment.entries} />
        </div>
      );
    default:
      return null;
  }
});
