export function buildUnifiedDiff(oldText: string, newText: string): string {
  const removed = oldText.length ? oldText.split('\n').map((l) => `-${l}`) : [];
  const added = newText.length ? newText.split('\n').map((l) => `+${l}`) : [];
  return [...removed, ...added].join('\n');
}
