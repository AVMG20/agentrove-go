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
  it('appears after five quiet seconds, with the turn time', () => {
    render(
      <StreamWorkingStatus
        streamStartTime={startedFourMinutesAgo()}
        activityKey="3:120"
        assistantTextIsActive={false}
      />,
    );
    expect(screen.queryByText(/Still working/)).toBeNull();

    act(() => vi.advanceTimersByTime(4999));
    expect(screen.queryByText(/Still working/)).toBeNull();

    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText(/Still working/)).toBeTruthy();
    expect(screen.getByText('· 4m 15s')).toBeTruthy();
  });

  it('resets the delay when activity arrives before it appears', () => {
    const start = startedFourMinutesAgo();
    const { rerender } = render(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="3:120"
        assistantTextIsActive={false}
      />,
    );
    act(() => vi.advanceTimersByTime(3000));
    rerender(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="3:180"
        assistantTextIsActive={false}
      />,
    );
    expect(screen.queryByText(/Still working/)).toBeNull();
    act(() => vi.advanceTimersByTime(4999));
    expect(screen.queryByText(/Still working/)).toBeNull();
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByText(/Still working/)).toBeTruthy();
  });

  it('stays visible across tool activity and clears when assistant text resumes', () => {
    const start = startedFourMinutesAgo();
    const { rerender } = render(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="3:120"
        assistantTextIsActive={false}
      />,
    );
    act(() => vi.advanceTimersByTime(5000));
    expect(screen.getByText(/Still working/)).toBeTruthy();

    rerender(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="4:0"
        assistantTextIsActive={false}
      />,
    );
    expect(screen.getByText(/Still working/)).toBeTruthy();

    rerender(
      <StreamWorkingStatus streamStartTime={start} activityKey="5:14" assistantTextIsActive />,
    );
    expect(screen.queryByText(/Still working/)).toBeNull();
  });

  it('waits again after assistant text closes the previous group', () => {
    const start = startedFourMinutesAgo();
    const { rerender } = render(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="3:120"
        assistantTextIsActive={false}
      />,
    );
    act(() => vi.advanceTimersByTime(5000));
    expect(screen.getByText(/Still working/)).toBeTruthy();

    rerender(
      <StreamWorkingStatus streamStartTime={start} activityKey="4:14" assistantTextIsActive />,
    );
    rerender(
      <StreamWorkingStatus
        streamStartTime={start}
        activityKey="5:0"
        assistantTextIsActive={false}
      />,
    );
    expect(screen.queryByText(/Still working/)).toBeNull();
    act(() => vi.advanceTimersByTime(5000));
    expect(screen.getByText(/Still working/)).toBeTruthy();
  });
});
