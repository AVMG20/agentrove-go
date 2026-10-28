// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MessageRenderer } from './MessageRenderer';
import { chatService } from '@/services/chatService';
import type { AssistantStreamEvent } from '@/types/chat.types';
import type { ToolEventPayload } from '@/types/tools.types';

// Stub the leaf renderer so the test observes MessageRenderer's wiring — segment
// order and the per-segment active-thinking/active-text flags — without pulling
// in markdown, tool registries, or the smooth-text animation.
vi.mock('./SegmentView', () => ({
  SegmentView: ({
    segment,
    isActiveThinking,
    isActiveText,
  }: {
    segment: { id: string; kind: string; text?: string };
    isActiveThinking: boolean;
    isActiveText: boolean;
  }) => (
    <div
      data-testid="segment"
      data-id={segment.id}
      data-kind={segment.kind}
      data-active-thinking={String(isActiveThinking)}
      data-active-text={String(isActiveText)}
    >
      {segment.text ?? ''}
    </div>
  ),
}));

// The completed-turn work trace collapses into WorkedRollup; expose it as a
// boundary so the trace/tail split is assertable.
vi.mock('./WorkedRollup', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./WorkedRollup')>()),
  WorkedRollup: ({ children, onExpand }: { children: ReactNode; onExpand?: () => void }) => (
    <div data-testid="rollup">
      <button type="button" onClick={onExpand}>
        Worked
      </button>
      {children}
    </div>
  ),
}));

vi.mock('@/services/chatService', () => ({
  chatService: { getMessageRender: vi.fn() },
}));

const tool = (over: Partial<ToolEventPayload> & { id: string }): ToolEventPayload => ({
  name: 'Bash',
  title: 'run',
  status: 'started',
  ...over,
});

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

function renderedOrder() {
  return screen.getAllByTestId('segment').map((el) => el.getAttribute('data-id'));
}

describe('MessageRenderer streaming wiring', () => {
  afterEach(cleanup);

  it('renders segments in emission order', () => {
    const events: AssistantStreamEvent[] = [
      { type: 'assistant_text', text: 'intro' },
      { type: 'tool_started', tool: tool({ id: 't1' }) },
      { type: 'assistant_text', text: 'outro' },
    ];
    render(<MessageRenderer events={events} isStreaming />, { wrapper });
    expect(renderedOrder()).toEqual(['text-0', 'tool-t1', 'text-1']);
  });

  it('marks only the trailing text segment active while its text streams in', () => {
    const events: AssistantStreamEvent[] = [
      { type: 'assistant_text', text: 'first' },
      { type: 'tool_started', tool: tool({ id: 't1' }) },
      { type: 'assistant_text', text: 'live tail' },
    ];
    render(<MessageRenderer events={events} isStreaming />, { wrapper });

    const segments = screen.getAllByTestId('segment');
    const byId = Object.fromEntries(segments.map((el) => [el.getAttribute('data-id'), el]));
    expect(byId['text-1'].getAttribute('data-active-text')).toBe('true');
    expect(byId['text-0'].getAttribute('data-active-text')).toBe('false');
    expect(byId['tool-t1'].getAttribute('data-active-thinking')).toBe('false');
  });

  it('marks the trailing thinking segment active while thinking streams', () => {
    render(
      <MessageRenderer
        events={[{ type: 'assistant_thinking', thinking: 'pondering' }]}
        isStreaming
      />,
      { wrapper },
    );
    const segment = screen.getByTestId('segment');
    expect(segment.getAttribute('data-kind')).toBe('thinking');
    expect(segment.getAttribute('data-active-thinking')).toBe('true');
  });

  it('never marks a segment active once the turn is no longer streaming', () => {
    const events: AssistantStreamEvent[] = [
      { type: 'assistant_thinking', thinking: 'done thinking' },
      { type: 'assistant_text', text: 'final answer' },
    ];
    render(<MessageRenderer events={events} isStreaming={false} />, { wrapper });
    for (const el of screen.getAllByTestId('segment')) {
      expect(el.getAttribute('data-active-thinking')).toBe('false');
      expect(el.getAttribute('data-active-text')).toBe('false');
    }
  });

  it('collapses the completed work trace into the rollup and leaves the final answer outside it', () => {
    const events: AssistantStreamEvent[] = [
      { type: 'assistant_thinking', thinking: 'plan' },
      { type: 'tool_started', tool: tool({ id: 't1' }) },
      { type: 'tool_completed', tool: tool({ id: 't1', status: 'completed' }) },
      { type: 'assistant_text', text: 'the answer' },
    ];
    render(<MessageRenderer events={events} isStreaming={false} />, { wrapper });

    const rollup = screen.getByTestId('rollup');
    const rollupIds = within(rollup)
      .getAllByTestId('segment')
      .map((el) => el.getAttribute('data-id'));
    expect(rollupIds).toEqual(['thinking-0', 'tool-t1']);

    // The final text segment renders after the rollup, not inside it.
    expect(within(rollup).queryByText('the answer')).toBeNull();
    expect(screen.getByText('the answer')).toBeTruthy();
  });

  it('keeps published artifacts outside the collapsed trace, ahead of the answer', () => {
    const artifact = { id: 'a1', name: 'mcp__agentrove__create_artifact' };
    const events: AssistantStreamEvent[] = [
      { type: 'tool_started', tool: tool({ id: 't1' }) },
      { type: 'tool_completed', tool: tool({ id: 't1', status: 'completed' }) },
      { type: 'tool_started', tool: tool(artifact) },
      { type: 'tool_completed', tool: tool({ ...artifact, status: 'completed' }) },
      { type: 'assistant_text', text: 'the answer' },
    ];
    render(<MessageRenderer events={events} isStreaming={false} />, { wrapper });

    const rollup = screen.getByTestId('rollup');
    const rollupIds = within(rollup)
      .getAllByTestId('segment')
      .map((el) => el.getAttribute('data-id'));
    expect(rollupIds).toEqual(['tool-t1']);
    expect(renderedOrder()).toEqual(['tool-t1', 'tool-a1', 'text-0']);
  });

  it('keeps live segments flat (no rollup) while streaming', () => {
    const events: AssistantStreamEvent[] = [
      { type: 'assistant_thinking', thinking: 'plan' },
      { type: 'tool_started', tool: tool({ id: 't1' }) },
      { type: 'assistant_text', text: 'streaming answer' },
    ];
    render(<MessageRenderer events={events} isStreaming />, { wrapper });
    expect(screen.queryByTestId('rollup')).toBeNull();
    expect(renderedOrder()).toEqual(['thinking-0', 'tool-t1', 'text-0']);
  });

  it('loads omitted tool results on expand and recovers from a failed load via Retry', async () => {
    const getMessageRender = vi.mocked(chatService.getMessageRender);
    const slim: AssistantStreamEvent[] = [
      { type: 'tool_completed', tool: tool({ id: 't1', status: 'completed' }) },
      { type: 'assistant_text', text: 'the answer' },
    ];
    const full: AssistantStreamEvent[] = [
      { type: 'tool_completed', tool: tool({ id: 't1', status: 'completed', result: 'out' }) },
      { type: 'assistant_text', text: 'the answer' },
    ];
    getMessageRender
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce({ events: full });
    render(
      <MessageRenderer
        events={slim}
        isStreaming={false}
        omittedToolResults={{ messageId: 'm1' }}
      />,
      { wrapper },
    );

    // Cards never render from the stripped events.
    const rollup = screen.getByTestId('rollup');
    expect(within(rollup).queryByTestId('segment')).toBeNull();
    expect(getMessageRender).not.toHaveBeenCalled();

    fireEvent.click(screen.getByText('Worked'));
    const retry = await screen.findByText('Retry');
    expect(screen.getByText('the answer')).toBeTruthy();
    fireEvent.click(retry);

    expect(await within(rollup).findByTestId('segment')).toBeTruthy();
    expect(screen.queryByText('Retry')).toBeNull();
    expect(screen.getByText('the answer')).toBeTruthy();
    expect(getMessageRender).toHaveBeenCalledTimes(2);
    expect(getMessageRender).toHaveBeenCalledWith('m1');
  });
});
