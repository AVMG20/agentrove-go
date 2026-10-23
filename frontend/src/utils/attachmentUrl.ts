const API_PREFIX = '/api/v1';
const ATTACHMENTS_PREFIX = `${API_PREFIX}/attachments/`;

function parseUrl(fileUrl: string): URL | null {
  try {
    return new URL(fileUrl, 'http://localhost');
  } catch {
    return null;
  }
}

export function isApiAttachmentUrl(fileUrl: string): boolean {
  const parsed = parseUrl(fileUrl);
  if (!parsed) return false;
  return parsed.pathname.startsWith(ATTACHMENTS_PREFIX);
}

export function toApiEndpoint(fileUrl: string): string {
  const parsed = parseUrl(fileUrl);
  if (!parsed || !parsed.pathname.startsWith(API_PREFIX)) {
    return fileUrl;
  }

  return `${parsed.pathname.slice(API_PREFIX.length)}${parsed.search}`;
}

export function toDownloadUrl(fileUrl: string): string {
  const parsed = parseUrl(fileUrl);
  if (!parsed) return fileUrl;

  if (!parsed.pathname.endsWith('/preview')) {
    return fileUrl;
  }

  const nextPathname = `${parsed.pathname.slice(0, -'/preview'.length)}/download`;
  if (fileUrl.startsWith('/')) {
    return `${nextPathname}${parsed.search}`;
  }
  return `${parsed.origin}${nextPathname}${parsed.search}`;
}

export function isBrowserObjectUrl(fileUrl: string): boolean {
  return fileUrl.startsWith('blob:') || fileUrl.startsWith('data:');
}
