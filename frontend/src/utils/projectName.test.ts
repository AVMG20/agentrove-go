import { describe, expect, it } from 'vitest';
import { projectNameFrom } from './projectName';

describe('projectNameFrom', () => {
  it('takes the repo from clone URLs', () => {
    expect(projectNameFrom('https://github.com/org/my-app.git')).toBe('my-app');
    expect(projectNameFrom('https://github.com/org/my-app/')).toBe('my-app');
    expect(projectNameFrom('git@github.com:org/api.git')).toBe('api');
    expect(projectNameFrom('git@github.com:solo.git')).toBe('solo');
  });

  it('takes the folder from a path', () => {
    expect(projectNameFrom('~/code/site/')).toBe('site');
    expect(projectNameFrom('  ')).toBe('');
  });
});
