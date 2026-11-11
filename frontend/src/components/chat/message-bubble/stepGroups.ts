import { ARTIFACT_TOOL_NAMES } from '@/components/chat/tools/registry';
import type { MessageSegment } from './segmentBuilder';

export type SegmentItem =
  | { kind: 'segment'; segment: MessageSegment }
  | { kind: 'steps'; id: string; segments: MessageSegment[] };

// Tools whose card is the point of the call (published artifacts, generated UI,
// the plan to approve) stay in the flow instead of folding into a step group.
const STANDALONE_TOOLS = new Set([
  ...ARTIFACT_TOOL_NAMES,
  'mcp__agentrove__render_ui',
  'ExitPlanMode',
]);

const isStep = (segment: MessageSegment) =>
  segment.kind === 'thinking' ||
  (segment.kind === 'tool' && !STANDALONE_TOOLS.has(segment.tool.name));

// Folds each run of two or more consecutive tool calls and thoughts into one
// group; text, steering, plans and standalone tools break a run. A group is
// keyed by its first segment so its open state survives as the run grows.
export function groupSteps(segments: MessageSegment[]): SegmentItem[] {
  const items: SegmentItem[] = [];
  let run: MessageSegment[] = [];
  const flush = () => {
    if (run.length > 1) items.push({ kind: 'steps', id: `steps-${run[0].id}`, segments: run });
    else if (run.length === 1) items.push({ kind: 'segment', segment: run[0] });
    run = [];
  };
  for (const segment of segments) {
    if (isStep(segment)) {
      run.push(segment);
      continue;
    }
    flush();
    items.push({ kind: 'segment', segment });
  }
  flush();
  return items;
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

export function stepsLabel(segments: MessageSegment[]): string {
  const thoughts = segments.filter((s) => s.kind === 'thinking').length;
  const tools = segments.length - thoughts;
  return [
    tools > 0 && plural(tools, 'tool call', 'tool calls'),
    thoughts > 0 && plural(thoughts, 'thought', 'thoughts'),
  ]
    .filter(Boolean)
    .join(', ');
}
