// @vitest-environment happy-dom

import { act, cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StreamWorkingStatus } from './StreamWorkingStatus';

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-01-01T00:10:00Z'));
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const startedFourMinutesAgo = () => Date.now() - (4 * 60 + 10) * 1000;

describe('StreamWorkingStatus', () => {
  it('appears once the reply has been quiet for a while, with the turn time', () => {
    render(<StreamWorkingStatus streamStartTime={startedFourMinutesAgo()} activityKey="3:120" />);
    expect(screen.queryByText(/Still working/)).toBeNull();

    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByText(/Still working/)).toBeTruthy();
    expect(screen.getByText('· 4m 12s')).toBeTruthy();
  });

  it('hides again as soon as more output arrives', () => {
    const start = startedFourMinutesAgo();
    const { rerender } = render(
      <StreamWorkingStatus streamStartTime={start} activityKey="3:120" />,
    );
    act(() => vi.advanceTimersByTime(3000));
    expect(screen.getByText(/Still working/)).toBeTruthy();

    rerender(<StreamWorkingStatus streamStartTime={start} activityKey="3:180" />);
    expect(screen.queryByText(/Still working/)).toBeNull();
  });
});
