// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { openExternalUrl } from './openExternal';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('openExternalUrl', () => {
  it('opens the url in a new tab', () => {
    const win = vi.spyOn(window, 'open').mockImplementation(() => null);

    openExternalUrl('https://example.com');

    expect(win).toHaveBeenCalledWith('https://example.com', '_blank');
  });
});
