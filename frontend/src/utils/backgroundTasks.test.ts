import { describe, expect, it } from 'vitest';
import { formatElapsed } from './backgroundTasks';

describe('formatElapsed', () => {
  it('drops units that carry no information', () => {
    expect(formatElapsed(-5)).toBe('0s');
    expect(formatElapsed(41_900)).toBe('41s');
    expect(formatElapsed(134_000)).toBe('2m 14s');
    expect(formatElapsed(3_900_000)).toBe('1h 05m');
  });
});
