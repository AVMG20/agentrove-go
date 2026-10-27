// Order matters: Edge and Opera also claim Chrome, and Chrome claims Safari.
const BROWSERS: [RegExp, string][] = [
  [/Edg(e|A|iOS)?\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/SamsungBrowser\//, 'Samsung Internet'],
  [/Firefox\/|FxiOS\//, 'Firefox'],
  [/Chrome\/|CriOS\//, 'Chrome'],
  [/Version\/[\d.]+.*Safari\//, 'Safari'],
];

const SYSTEMS: [RegExp, string][] = [
  [/iPhone/, 'iPhone'],
  [/iPad/, 'iPad'],
  [/Android/, 'Android'],
  [/Windows/, 'Windows'],
  [/CrOS/, 'ChromeOS'],
  [/Mac OS X|Macintosh/, 'macOS'],
  [/Linux/, 'Linux'],
];

const match = (ua: string, table: [RegExp, string][]) =>
  table.find(([pattern]) => pattern.test(ua))?.[1];

// A short "Browser on OS" label for a session's user agent.
export function describeUserAgent(ua: string): string {
  const browser = match(ua, BROWSERS);
  const system = match(ua, SYSTEMS);
  if (browser && system) return `${browser} on ${system}`;
  return browser ?? system ?? 'Unknown device';
}

export function isMobileUserAgent(ua: string): boolean {
  return /iPhone|iPad|Android|Mobile/.test(ua);
}
