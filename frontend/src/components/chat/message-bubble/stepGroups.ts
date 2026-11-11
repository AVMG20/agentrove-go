import { ARTIFACT_TOOL_NAMES } from '@/components/chat/tools/registry';
import { formatDuration } from '@/utils/date';
import type { MessageSegment } from './segmentBuilder';

export type SegmentItem =
  | { kind: 'segment'; segment: MessageSegment }
  // durationMs: from the run's first step to whatever came next, when both
  // carry a timestamp. A trailing run has none: it is live or ended the turn.
  | { kind: 'steps'; id: string; segments: MessageSegment[]; durationMs?: number };

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
// `liveTail` folds a trailing run from its first step, so a live run is a group
// from the start instead of turning from a card into one at its second step.
export function groupSteps(segments: MessageSegment[], liveTail = false): SegmentItem[] {
  const items: SegmentItem[] = [];
  let run: MessageSegment[] = [];
  const flush = (minSteps = 2, next?: MessageSegment) => {
    if (run.length >= minSteps) {
      const start = run[0].startedAt;
      const end = next?.startedAt;
      const durationMs = start != null && end != null && end >= start ? end - start : undefined;
      items.push({ kind: 'steps', id: `steps-${run[0].id}`, segments: run, durationMs });
    } else if (run.length === 1) {
      items.push({ kind: 'segment', segment: run[0] });
    }
    run = [];
  };
  for (const segment of segments) {
    if (isStep(segment)) {
      run.push(segment);
      continue;
    }
    flush(2, segment);
    items.push({ kind: 'segment', segment });
  }
  flush(liveTail ? 1 : 2);
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

// Older messages persisted before duration tracking have no duration, and a
// sub-second one would read "0s".
export const workedLabel = (durationMs: number | null | undefined): string =>
  durationMs != null && durationMs >= 500 ? `Worked for ${formatDuration(durationMs)}` : 'Worked';
