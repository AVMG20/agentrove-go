// Suggested project name from what's being opened: the repo of a clone URL
// (https or git@) or the last segment of a folder path.
export function projectNameFrom(source: string): string {
  const trimmed = source.trim().replace(/[/\\]+$/, '');
  const last = trimmed.slice(Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf(':')) + 1);
  return last.replace(/\.git$/, '');
}
