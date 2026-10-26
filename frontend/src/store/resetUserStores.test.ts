// @vitest-environment jsdom
import { describe, it, expect, vi } from 'vitest';

vi.mock('@/services/queueService', () => ({ queueService: {} }));

import { resetUserScopedStores } from './resetUserStores';
import { useStreamStore } from './streamStore';
import { useMessageQueueStore } from './messageQueueStore';
import { usePermissionStore } from './permissionStore';
import type { ActiveStream } from '@/types/stream.types';

describe('resetUserScopedStores', () => {
  it('drops the previous account streams, queues and pending permissions', () => {
    const stream: ActiveStream = {
      id: 's1',
      chatId: 'c1',
      messageId: 'm1',
      startTime: 1,
      isActive: true,
      callbacks: {},
    };
    useStreamStore.getState().addStream(stream);
    useMessageQueueStore.setState({
      queues: new Map([
        ['c1', [{ id: 'q1', content: 'x', queuedAt: 1, synced: true, sendingNow: false }]],
      ]),
    });
    usePermissionStore.getState().enqueuePermissionRequest('c1', {
      request_id: 'r1',
      tool_name: 'Bash',
      tool_input: {},
      options: [],
    } as never);

    resetUserScopedStores();

    expect(useStreamStore.getState().activeStreams.size).toBe(0);
    expect(useStreamStore.getState().activeStreamMetadata).toEqual([]);
    // The shared SSE manager keys off isActive, so a retained reference must not look live.
    expect(stream.isActive).toBe(false);
    expect(useMessageQueueStore.getState().queues.size).toBe(0);
    expect(usePermissionStore.getState().pendingRequests.size).toBe(0);
  });
});
