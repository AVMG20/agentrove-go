import { describe, it, expect, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import { ALL_COMMANDS, executeCommand } from './commandRegistry';
import { queryKeys } from '@/hooks/queries/queryKeys';

vi.mock('react-hot-toast', () => ({ default: { success: vi.fn(), error: vi.fn() } }));
vi.mock('@/services/sandboxService', () => ({
  sandboxService: { gitPull: vi.fn(async () => ({ success: true, output: '' })) },
}));

describe('pull-remote', () => {
  // An open tab holding pre-pull content would otherwise save over the pulled files.
  it('invalidates cached file contents', async () => {
    const queryClient = new QueryClient();
    queryClient.setQueryData(queryKeys.sandbox.fileContent('sbx', 'a.txt'), { content: 'old' });
    const pull = ALL_COMMANDS.find((c) => c.id === 'pull-remote')!;

    executeCommand(pull, queryClient, vi.fn(), false, { sandboxId: 'sbx' });

    await vi.waitFor(() => {
      expect(
        queryClient.getQueryState(queryKeys.sandbox.fileContent('sbx', 'a.txt'))?.isInvalidated,
      ).toBe(true);
    });
  });
});
