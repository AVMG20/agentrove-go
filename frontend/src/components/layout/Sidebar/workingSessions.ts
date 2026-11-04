// Sessions the sidebar's "Working" card follows: every chat seen working, kept
// as Done after it finishes until it's opened (which clears its completion).
export interface TrackedSession {
  chatId: string;
  startedAt: number;
  // Set when the work stops; the card then shows it as Done with a frozen timer.
  endedAt: number | null;
}

// The card only floats in once at least this many sessions are tracked.
export const MIN_WORKING_CARD_SESSIONS = 2;

// Returns `prev` untouched when nothing changed, so it can feed setState directly.
export function reconcileSessions(
  prev: TrackedSession[],
  working: ReadonlySet<string>,
  completed: ReadonlySet<string>,
  startTimeByChat: ReadonlyMap<string, number>,
  now: number,
): TrackedSession[] {
  let changed = false;
  const next: TrackedSession[] = [];
  for (const session of prev) {
    const { chatId } = session;
    if (working.has(chatId)) {
      if (session.endedAt === null) {
        next.push(session);
      } else {
        // A Done session picked up a new turn: it's working again, timer restarts.
        next.push({ chatId, startedAt: startTimeByChat.get(chatId) ?? now, endedAt: null });
        changed = true;
      }
    } else if (completed.has(chatId)) {
      if (session.endedAt === null) {
        next.push({ ...session, endedAt: now });
        changed = true;
      } else {
        next.push(session);
      }
    } else {
      // Opened after finishing, cancelled or failed: it leaves the card.
      changed = true;
    }
  }
  const known = new Set(prev.map((session) => session.chatId));
  for (const chatId of working) {
    if (known.has(chatId)) continue;
    next.push({ chatId, startedAt: startTimeByChat.get(chatId) ?? now, endedAt: null });
    changed = true;
  }
  return changed ? next : prev;
}

// Still-running sessions first, then the finished ones, each oldest first.
export function orderSessions(sessions: TrackedSession[]): TrackedSession[] {
  return [...sessions].sort(
    (a, b) => Number(a.endedAt !== null) - Number(b.endedAt !== null) || a.startedAt - b.startedAt,
  );
}
