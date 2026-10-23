import { describe, expect, it } from 'vitest';
import { suggestEnvVarName } from './envVarNames';

describe('suggestEnvVarName', () => {
  it('suggests the real name for near misses', () => {
    expect(suggestEnvVarName('ANTHROPIC_AUTHTOKEN')).toBe('ANTHROPIC_AUTH_TOKEN');
    expect(suggestEnvVarName('ANTROPIC_BASE_URL')).toBe('ANTHROPIC_BASE_URL');
    expect(suggestEnvVarName('anthropic_api_kye')).toBe('ANTHROPIC_API_KEY');
  });

  it('stays quiet for known or unrelated names', () => {
    expect(suggestEnvVarName('ANTHROPIC_AUTH_TOKEN')).toBeNull();
    expect(suggestEnvVarName('OPENAI_API_KEY')).toBeNull();
    expect(suggestEnvVarName('DATABASE_URL')).toBeNull();
    expect(suggestEnvVarName('FOO')).toBeNull();
  });
});
