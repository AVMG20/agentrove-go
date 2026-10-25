// Variables a provider profile sets (credentials, endpoint, usage URL, config
// dirs); the server refuses them as env vars (profiles.managedEnv).
export const PROFILE_MANAGED_ENV_VARS = [
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_AUTH_TOKEN',
  'CLAUDE_CODE_OAUTH_TOKEN',
  'ANTHROPIC_BASE_URL',
  'CLAUDE_USAGE_URL',
  'CLAUDE_CONFIG_DIR',
  'CODEX_HOME',
] as const;

export function isProfileManagedEnvVar(key: string): boolean {
  return (PROFILE_MANAGED_ENV_VARS as readonly string[]).includes(key.trim().toUpperCase());
}

// Variables Claude Code reads for auth and routing. A near-miss name (e.g.
// ANTHROPIC_AUTHTOKEN) is silently ignored and the session fails with
// "Not logged in", so the env var dialog suggests the real name.
export const KNOWN_CLAUDE_ENV_VARS = [
  'ANTHROPIC_API_KEY',
  'ANTHROPIC_AUTH_TOKEN',
  'ANTHROPIC_BASE_URL',
  'ANTHROPIC_MODEL',
  'ANTHROPIC_SMALL_FAST_MODEL',
  'ANTHROPIC_DEFAULT_OPUS_MODEL',
  'ANTHROPIC_DEFAULT_SONNET_MODEL',
  'ANTHROPIC_DEFAULT_HAIKU_MODEL',
  'ANTHROPIC_CUSTOM_HEADERS',
  'CLAUDE_CODE_OAUTH_TOKEN',
  'CLAUDE_CODE_USE_BEDROCK',
  'CLAUDE_CODE_USE_VERTEX',
  'CLAUDE_CODE_MAX_OUTPUT_TOKENS',
  'API_TIMEOUT_MS',
  // Bitbox's usage meter, for proxies that serve usage off <base URL>/api/usage.
  'CLAUDE_USAGE_URL',
  'GITHUB_TOKEN',
  'GH_TOKEN',
] as const;

function editDistance(a: string, b: string): number {
  const prev = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let diag = prev[0];
    prev[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const tmp = prev[j];
      prev[j] = Math.min(prev[j] + 1, prev[j - 1] + 1, diag + (a[i - 1] === b[j - 1] ? 0 : 1));
      diag = tmp;
    }
  }
  return prev[b.length];
}

/** The known variable `key` most likely meant, or null if it is known or not close to one. */
export function suggestEnvVarName(key: string): string | null {
  const k = key.trim().toUpperCase();
  if (k.length < 6 || (KNOWN_CLAUDE_ENV_VARS as readonly string[]).includes(k)) return null;
  const squash = (s: string) => s.replace(/_/g, '');
  let best: string | null = null;
  let bestDist = Infinity;
  for (const name of KNOWN_CLAUDE_ENV_VARS) {
    const d = squash(name) === squash(k) ? 0 : editDistance(name, k);
    if (d < bestDist) {
      best = name;
      bestDist = d;
    }
  }
  return bestDist <= 2 ? best : null;
}
