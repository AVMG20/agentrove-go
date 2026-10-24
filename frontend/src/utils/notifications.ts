import type { ElicitationRequest, PermissionRequest } from '@/types/chat.types';
import { logger } from '@/utils/logger';

// Dedup OS notifications when SSE envelopes replay on reconnect.
const NOTIFIED_PERMISSION_REQUESTS = new Set<string>();
const NOTIFIED_ELICITATION_REQUESTS = new Set<string>();

// The OS clips long bodies anyway.
const BODY_MAX_CHARS = 200;

// Notify-or-not policy lives in useBackgroundNotify; this module formats and sends.
export interface NotifyOptions {
  // The assistant's reply — becomes the notification body.
  message?: string;
  // Invoked after focusing the window.
  onClick?: () => void;
}

function buildPermissionNotification(request: PermissionRequest): {
  title: string;
  body: string;
} {
  switch (request.tool_name) {
    case 'ExitPlanMode':
      return {
        title: 'Plan ready for approval',
        body: 'Review the plan and approve or reject it.',
      };
    default:
      return {
        title: 'Permission needed',
        body: `Tool "${request.tool_name}" is waiting for your approval.`,
      };
  }
}

// Resolves to whether a notification was actually shown.
async function deliver(title: string, body: string, onClick?: () => void): Promise<boolean> {
  if (typeof window === 'undefined' || !('Notification' in window)) {
    return false;
  }

  // Prompting here would be ignored outside a user gesture — useNotificationPermissionPrompt asks.
  if (Notification.permission !== 'granted') {
    return false;
  }

  const notification = new Notification(title, { body });
  notification.onclick = () => {
    window.focus();
    onClick?.();
    notification.close();
  };
  return true;
}

// Call from a user gesture so the browser honors the prompt.
export async function requestNotificationPermission(): Promise<void> {
  try {
    if (
      typeof window !== 'undefined' &&
      'Notification' in window &&
      Notification.permission === 'default'
    ) {
      await Notification.requestPermission();
    }
  } catch (error) {
    logger.debug('Failed to request notification permission', 'notifications', error);
  }
}

// Notifications the user hasn't come back to yet.
let unseenNotifications = 0;

async function syncAppBadge(count: number): Promise<void> {
  try {
    // Badging API only has an effect for installed PWAs.
    if ('setAppBadge' in navigator) {
      if (count > 0) {
        await navigator.setAppBadge(count);
      } else {
        await navigator.clearAppBadge();
      }
    }
  } catch (error) {
    logger.debug('Failed to sync app badge', 'notifications', error);
  }
}

export async function bumpAppBadge(): Promise<void> {
  unseenNotifications += 1;
  await syncAppBadge(unseenNotifications);
}

export async function resetAppBadge(): Promise<void> {
  if (unseenNotifications === 0) return;
  unseenNotifications = 0;
  await syncAppBadge(0);
}

function clip(text: string): string {
  return text.length > BODY_MAX_CHARS ? `${text.slice(0, BODY_MAX_CHARS).trimEnd()}…` : text;
}

function assistantBody(message: string | undefined): string {
  const text = message?.trim();
  return text ? clip(text) : 'The assistant has finished responding.';
}

export async function notifyStreamComplete(options: NotifyOptions = {}): Promise<boolean> {
  try {
    return await deliver('Task completed', assistantBody(options.message), options.onClick);
  } catch (error) {
    logger.debug('Failed to send stream complete notification', 'notifications', error);
    return false;
  }
}

// The turn failed; options.message is the reason.
export async function notifyStreamFailed(options: NotifyOptions = {}): Promise<boolean> {
  try {
    const reason = options.message?.trim();
    return await deliver(
      'Task failed',
      reason ? clip(reason) : 'The turn ended with an error.',
      options.onClick,
    );
  } catch (error) {
    logger.debug('Failed to send stream failed notification', 'notifications', error);
    return false;
  }
}

// One notification per request id. Claimed synchronously so concurrent replays
// can't double-notify, and released if nothing was shown so a later replay can.
async function notifyOnce(
  notified: Set<string>,
  requestId: string,
  title: string,
  body: string,
  onClick: (() => void) | undefined,
): Promise<boolean> {
  if (notified.has(requestId)) {
    return false;
  }
  notified.add(requestId);

  try {
    const delivered = await deliver(title, body, onClick);
    if (!delivered) {
      notified.delete(requestId);
    }
    return delivered;
  } catch (error) {
    notified.delete(requestId);
    logger.debug('Failed to send request notification', 'notifications', error);
    return false;
  }
}

export async function notifyPermissionRequest(
  request: PermissionRequest,
  options: NotifyOptions = {},
): Promise<boolean> {
  const { title, body } = buildPermissionNotification(request);
  return notifyOnce(NOTIFIED_PERMISSION_REQUESTS, request.request_id, title, body, options.onClick);
}

// AskUserQuestion and MCP elicitations: the agent is blocked on an answer.
export async function notifyElicitationRequest(
  request: ElicitationRequest,
  options: NotifyOptions = {},
): Promise<boolean> {
  const question = request.message.trim();
  return notifyOnce(
    NOTIFIED_ELICITATION_REQUESTS,
    request.request_id,
    'Question for you',
    question ? clip(question) : 'The agent is waiting for your answer.',
    options.onClick,
  );
}
