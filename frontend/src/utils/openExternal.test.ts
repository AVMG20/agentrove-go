// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { openExternalUrl, toSafeHttpUrl } from './openExternal';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('openExternalUrl', () => {
  it('opens the url in a new tab without leaking the opener', () => {
    const win = vi.spyOn(window, 'open').mockImplementation(() => null);

    openExternalUrl('https://example.com');

    expect(win).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer');
  });

  it('refuses non-http(s) urls', () => {
    const win = vi.spyOn(window, 'open').mockImplementation(() => null);

    openExternalUrl('javascript:alert(1)');

    expect(win).not.toHaveBeenCalled();
  });
});

describe('toSafeHttpUrl', () => {
  it('accepts http and https urls', () => {
    expect(toSafeHttpUrl('https://example.com/a?b=1')).toBe('https://example.com/a?b=1');
    expect(toSafeHttpUrl('http://example.com')).toBe('http://example.com');
  });

  it.each([
    'javascript:alert(1)',
    'JaVaScRiPt:alert(1)',
    '  javascript:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'file:///etc/passwd',
    '/relative/path',
    'not a url',
    '',
  ])('rejects %j', (url) => {
    expect(toSafeHttpUrl(url)).toBeNull();
  });

  it('rejects non-string input from untrusted JSON', () => {
    expect(toSafeHttpUrl(undefined)).toBeNull();
    expect(toSafeHttpUrl({ href: 'https://example.com' })).toBeNull();
  });
});
