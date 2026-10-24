// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TurnStatusFooter } from './TurnStatusFooter';
import { buildFailedMessageUpdate } from '@/hooks/stream/messageUpdates';
import type { Message } from '@/types/chat.types';

afterEach(cleanup);

describe('TurnStatusFooter', () => {
  it('shows a failed turn with its reason and offers Retry and Continue', () => {
    const onRetry = vi.fn();
    const onContinue = vi.fn();
    render(
      <TurnStatusFooter
        status="failed"
        error="rate limited"
        onRetry={onRetry}
        onContinue={onContinue}
      />,
    );
    expect(screen.getByRole('status').textContent).toContain('Failed: rate limited');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
    expect(onContinue).toHaveBeenCalledTimes(1);
  });

  it('says only "Failed" for older messages whose reason is in the text already', () => {
    render(<TurnStatusFooter status="failed" error={null} onContinue={vi.fn()} />);
    expect(screen.getByRole('status').textContent).toMatch(/^Failed(Retry|Continue)/);
  });

  it('marks a stopped or cut-off turn as stopped', () => {
    render(<TurnStatusFooter status="interrupted" onRetry={vi.fn()} onContinue={vi.fn()} />);
    expect(screen.getByText('Stopped')).toBeTruthy();
  });

  it('hides Retry when there is no prompt to resend', () => {
    render(<TurnStatusFooter status="interrupted" onContinue={vi.fn()} />);
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });
});

describe('buildFailedMessageUpdate', () => {
  it('records the reason as stream_error instead of appending it to the reply', () => {
    const msg = {
      id: 'm1',
      content_text: 'Partial',
      content_render: { events: [{ type: 'assistant_text', text: 'Partial' }] },
      stream_status: 'in_progress',
      active_stream_id: 's1',
    } as Message;
    const failed = buildFailedMessageUpdate(new Error('boom'))(msg);
    expect(failed.stream_status).toBe('failed');
    expect(failed.stream_error).toBe('boom');
    expect(failed.content_render.events).toEqual(msg.content_render.events);
    expect(failed.content_text).toBe('Partial\n\nError: boom');
  });
});
