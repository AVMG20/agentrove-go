// Agent- and web-supplied URLs (search results, tool output) are untrusted: only
// http(s) may become a link target, never javascript:/data:/file: and friends.
export function toSafeHttpUrl(url: unknown): string | null {
  if (typeof url !== 'string') return null;
  try {
    const { protocol } = new URL(url.trim());
    return protocol === 'http:' || protocol === 'https:' ? url.trim() : null;
  } catch {
    return null;
  }
}

export function openExternalUrl(url: string): void {
  const safeUrl = toSafeHttpUrl(url);
  if (!safeUrl) return;
  window.open(safeUrl, '_blank', 'noopener,noreferrer');
}
