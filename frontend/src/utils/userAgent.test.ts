import { describe, expect, it } from 'vitest';
import { describeUserAgent, isMobileUserAgent } from './userAgent';

const UA = {
  chromeMac:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36',
  edgeWindows:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0',
  firefoxLinux: 'Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0',
  safariIphone:
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  chromeAndroid:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36',
};

describe('describeUserAgent', () => {
  it.each([
    [UA.chromeMac, 'Chrome on macOS'],
    [UA.edgeWindows, 'Edge on Windows'],
    [UA.firefoxLinux, 'Firefox on Linux'],
    [UA.safariIphone, 'Safari on iPhone'],
    [UA.chromeAndroid, 'Chrome on Android'],
  ])('labels %s', (ua, label) => {
    expect(describeUserAgent(ua)).toBe(label);
  });

  it('falls back for unknown or missing agents', () => {
    expect(describeUserAgent('curl/8.5.0')).toBe('Unknown device');
    expect(describeUserAgent('')).toBe('Unknown device');
  });
});

describe('isMobileUserAgent', () => {
  it('tells phones from desktops', () => {
    expect(isMobileUserAgent(UA.safariIphone)).toBe(true);
    expect(isMobileUserAgent(UA.chromeAndroid)).toBe(true);
    expect(isMobileUserAgent(UA.chromeMac)).toBe(false);
  });
});
