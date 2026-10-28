// @vitest-environment happy-dom

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { ElicitationRequest, PermissionRequest } from '@/types/chat.types';

const { debug } = vi.hoisted(() => ({ debug: vi.fn() }));

vi.mock('@/utils/logger', () => ({ logger: { debug } }));

class FakeNotification {
  static permission = 'granted';
  static instances: FakeNotification[] = [];
  onclick: (() => void) | null = null;
  close = vi.fn();
  constructor(
    public title: string,
    public options?: { body?: string },
  ) {
    FakeNotification.instances.push(this);
  }
}

// Fresh module per test so the module-level dedup Set and badge count reset.
async function load() {
  vi.resetModules();
  return import('./notifications');
}

const request = (overrides: Partial<PermissionRequest> = {}): PermissionRequest =>
  ({ request_id: 'r1', tool_name: 'Bash', ...overrides }) as PermissionRequest;

const shown = () => FakeNotification.instances.map((n) => ({ title: n.title, ...n.options }));
const lastShown = () => shown()[FakeNotification.instances.length - 1];

beforeEach(() => {
  debug.mockClear();
  FakeNotification.permission = 'granted';
  FakeNotification.instances = [];
  vi.stubGlobal('Notification', FakeNotification);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('notifyPermissionRequest', () => {
  it('sends a generic permission notification for an arbitrary tool', async () => {
    const { notifyPermissionRequest } = await load();
    await notifyPermissionRequest(request({ tool_name: 'Bash' }));

    expect(shown()).toEqual([
      { title: 'Permission needed', body: 'Tool "Bash" is waiting for your approval.' },
    ]);
  });

  it('uses plan-specific copy for ExitPlanMode', async () => {
    const { notifyPermissionRequest } = await load();
    await notifyPermissionRequest(request({ tool_name: 'ExitPlanMode' }));

    expect(shown()).toEqual([
      { title: 'Plan ready for approval', body: 'Review the plan and approve or reject it.' },
    ]);
  });

  it('deduplicates repeat notifications for the same request id', async () => {
    const { notifyPermissionRequest } = await load();
    await notifyPermissionRequest(request({ request_id: 'dup' }));
    await notifyPermissionRequest(request({ request_id: 'dup' }));

    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('releases the dedupe key while permission is undecided', async () => {
    FakeNotification.permission = 'default';
    const { notifyPermissionRequest } = await load();
    await expect(notifyPermissionRequest(request({ request_id: 'pending' }))).resolves.toBe(false);
    expect(FakeNotification.instances).toHaveLength(0);

    // User granted via the first-gesture prompt; a replay must still notify…
    FakeNotification.permission = 'granted';
    await notifyPermissionRequest(request({ request_id: 'pending' }));
    expect(FakeNotification.instances).toHaveLength(1);

    // …and only once.
    await notifyPermissionRequest(request({ request_id: 'pending' }));
    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('swallows notification errors and logs them', async () => {
    vi.stubGlobal(
      'Notification',
      class {
        static permission = 'granted';
        constructor() {
          throw new Error('nope');
        }
      },
    );
    const { notifyPermissionRequest } = await load();

    await expect(notifyPermissionRequest(request())).resolves.toBe(false);
    expect(debug).toHaveBeenCalledTimes(1);
  });
});

describe('app badge', () => {
  const setAppBadge = vi.fn<(count?: number) => Promise<void>>(async () => {});
  const clearAppBadge = vi.fn(async () => {});

  beforeEach(() => {
    setAppBadge.mockReset();
    setAppBadge.mockResolvedValue(undefined);
    clearAppBadge.mockReset();
    clearAppBadge.mockResolvedValue(undefined);
    Object.assign(navigator, { setAppBadge, clearAppBadge });
  });

  afterEach(() => {
    delete (navigator as { setAppBadge?: unknown }).setAppBadge;
    delete (navigator as { clearAppBadge?: unknown }).clearAppBadge;
  });

  it('counts up with each delivered notification', async () => {
    const { bumpAppBadge } = await load();
    await bumpAppBadge();
    await bumpAppBadge();

    expect(setAppBadge).toHaveBeenNthCalledWith(1, 1);
    expect(setAppBadge).toHaveBeenNthCalledWith(2, 2);
  });

  it('clears the badge on reset and starts over afterwards', async () => {
    const { bumpAppBadge, resetAppBadge } = await load();
    await bumpAppBadge();
    await resetAppBadge();
    expect(clearAppBadge).toHaveBeenCalledTimes(1);

    await bumpAppBadge();
    expect(setAppBadge).toHaveBeenLastCalledWith(1);
  });

  it('skips the reset round-trip when nothing is badged', async () => {
    const { resetAppBadge } = await load();
    await resetAppBadge();

    expect(clearAppBadge).not.toHaveBeenCalled();
  });

  it('swallows badge errors and logs them', async () => {
    setAppBadge.mockRejectedValueOnce(new Error('nope'));
    const { bumpAppBadge } = await load();

    await expect(bumpAppBadge()).resolves.toBeUndefined();
    expect(debug).toHaveBeenCalledTimes(1);
  });
});

describe('notifyStreamComplete', () => {
  it('sends a completion notification', async () => {
    const { notifyStreamComplete } = await load();
    await notifyStreamComplete();

    expect(shown()).toEqual([
      { title: 'Task completed', body: 'The assistant has finished responding.' },
    ]);
  });

  it("uses the assistant's reply as the body", async () => {
    const { notifyStreamComplete } = await load();
    await notifyStreamComplete({ message: '  Renamed the auth guard and updated its tests.  ' });

    expect(shown()).toEqual([
      { title: 'Task completed', body: 'Renamed the auth guard and updated its tests.' },
    ]);
  });

  it('truncates a long reply and falls back when the reply is empty', async () => {
    const { notifyStreamComplete } = await load();
    await notifyStreamComplete({ message: 'x'.repeat(250) });
    expect(lastShown()).toEqual({ title: 'Task completed', body: `${'x'.repeat(200)}…` });

    await notifyStreamComplete({ message: '   ' });
    expect(lastShown()).toEqual({
      title: 'Task completed',
      body: 'The assistant has finished responding.',
    });
  });

  // Pins deliberate behavior: OS-default auto-dismiss, click → focus + route + close.
  it('sends a plain auto-dismissing notification whose click focuses and routes', async () => {
    const focus = vi.spyOn(window, 'focus').mockImplementation(() => {});

    try {
      const { notifyStreamComplete } = await load();
      const onClick = vi.fn();
      await notifyStreamComplete({ message: 'Done.', onClick });

      const [notification] = FakeNotification.instances;
      expect(FakeNotification.instances).toHaveLength(1);
      expect(notification.title).toBe('Task completed');
      expect(notification.options).toEqual({ body: 'Done.' });

      notification.onclick?.();
      expect(focus).toHaveBeenCalledTimes(1);
      expect(onClick).toHaveBeenCalledTimes(1);
      expect(notification.close).toHaveBeenCalledTimes(1);
    } finally {
      focus.mockRestore();
    }
  });
});

describe('requestNotificationPermission', () => {
  it('prompts the browser while permission is undecided', async () => {
    const browserRequest = vi.fn(async () => 'granted');
    vi.stubGlobal('Notification', { permission: 'default', requestPermission: browserRequest });

    const { requestNotificationPermission } = await load();
    await requestNotificationPermission();
    expect(browserRequest).toHaveBeenCalledTimes(1);
  });

  it('does nothing once permission is decided', async () => {
    const browserRequest = vi.fn(async () => 'granted');
    vi.stubGlobal('Notification', { permission: 'denied', requestPermission: browserRequest });

    const { requestNotificationPermission } = await load();
    await requestNotificationPermission();
    expect(browserRequest).not.toHaveBeenCalled();
  });
});

describe('notifyElicitationRequest', () => {
  const question = (id: string, message: string) =>
    ({ request_id: id, message, tool_call_id: null, requested_schema: {} }) as ElicitationRequest;

  it('shows the agent question once per request id', async () => {
    const { notifyElicitationRequest } = await load();
    await notifyElicitationRequest(question('q1', 'Which database should I use?'));
    await notifyElicitationRequest(question('q1', 'Which database should I use?'));

    expect(shown()).toEqual([{ title: 'Question for you', body: 'Which database should I use?' }]);
  });

  it('falls back to generic copy for an empty question', async () => {
    const { notifyElicitationRequest } = await load();
    await notifyElicitationRequest(question('q2', '  '));
    expect(lastShown()).toEqual({
      title: 'Question for you',
      body: 'The agent is waiting for your answer.',
    });
  });
});

describe('notifyStreamFailed', () => {
  it('shows the failure reason', async () => {
    const { notifyStreamFailed } = await load();
    await notifyStreamFailed({ message: 'rate limited' });
    expect(lastShown()).toEqual({ title: 'Task failed', body: 'rate limited' });
  });
});
