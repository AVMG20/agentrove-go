import { describe, expect, it } from 'vitest';
import { orderSessions, reconcileSessions, type TrackedSession } from './workingSessions';

const none = new Set<string>();
const noStarts = new Map<string, number>();

describe('reconcileSessions', () => {
  it('tracks newly working chats with their stream start time', () => {
    const next = reconcileSessions([], new Set(['a', 'b']), none, new Map([['a', 5]]), 100);
    expect(next).toEqual([
      { chatId: 'a', startedAt: 5, endedAt: null },
      { chatId: 'b', startedAt: 100, endedAt: null },
    ]);
  });

  it('returns the same array when nothing changed', () => {
    const prev: TrackedSession[] = [{ chatId: 'a', startedAt: 1, endedAt: null }];
    expect(reconcileSessions(prev, new Set(['a']), none, noStarts, 50)).toBe(prev);
  });

  it('keeps a finished chat as done until its completion is cleared', () => {
    const prev: TrackedSession[] = [{ chatId: 'a', startedAt: 1, endedAt: null }];
    const done = reconcileSessions(prev, none, new Set(['a']), noStarts, 12);
    expect(done).toEqual([{ chatId: 'a', startedAt: 1, endedAt: 12 }]);
    expect(reconcileSessions(done, none, new Set(['a']), noStarts, 99)).toBe(done);
    // Opening the chat clears its completion.
    expect(reconcileSessions(done, none, none, noStarts, 100)).toEqual([]);
  });

  it('drops a chat that stopped without completing', () => {
    const prev: TrackedSession[] = [{ chatId: 'a', startedAt: 1, endedAt: null }];
    expect(reconcileSessions(prev, none, none, noStarts, 5)).toEqual([]);
  });

  it('restarts the timer when a done chat starts working again', () => {
    const prev: TrackedSession[] = [{ chatId: 'a', startedAt: 1, endedAt: 9 }];
    expect(reconcileSessions(prev, new Set(['a']), none, new Map([['a', 20]]), 30)).toEqual([
      { chatId: 'a', startedAt: 20, endedAt: null },
    ]);
  });
});

describe('orderSessions', () => {
  it('puts running sessions before done ones', () => {
    const ordered = orderSessions([
      { chatId: 'done', startedAt: 1, endedAt: 5 },
      { chatId: 'late', startedAt: 9, endedAt: null },
      { chatId: 'early', startedAt: 2, endedAt: null },
    ]);
    expect(ordered.map((s) => s.chatId)).toEqual(['early', 'late', 'done']);
  });
});
