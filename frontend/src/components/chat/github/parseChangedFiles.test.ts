import { describe, it, expect } from 'vitest';
import { parseChangedFiles } from './parseChangedFiles';

describe('parseChangedFiles', () => {
  it('counts content lines that start with ++ or --', () => {
    const diff = [
      'diff --git a/run.sh b/run.sh',
      'index 1111111..2222222 100644',
      '--- a/run.sh',
      '+++ b/run.sh',
      '@@ -1,2 +1,2 @@',
      '---verbose',
      '+++i',
      ' echo done',
      'diff --git a/new.txt b/new.txt',
      'new file mode 100644',
      '--- /dev/null',
      '+++ b/new.txt',
      '@@ -0,0 +1 @@',
      '+hello',
      '',
    ].join('\n');
    expect(parseChangedFiles(diff)).toEqual([
      { path: 'run.sh', additions: 1, deletions: 1 },
      { path: 'new.txt', additions: 1, deletions: 0 },
    ]);
  });
});
