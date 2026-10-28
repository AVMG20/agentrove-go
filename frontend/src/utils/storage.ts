import { logger } from '@/utils/logger';

const AUTH_TOKEN_KEY = 'auth_token';
const CHAT_EVENT_ID_PREFIX = 'chat:';
const CHAT_EVENT_ID_SUFFIX = ':lastEventId';
const MAX_CHAT_EVENT_ID_ENTRIES = 500;

const getStorage = (): Storage | null => {
  if (typeof window === 'undefined') {
    return null;
  }

  try {
    return window.localStorage;
  } catch (error) {
    logger.error('LocalStorage access failed', 'storage.getStorage', error);
    return null;
  }
};

export const safeGetItem = (key: string): string | null => {
  const storageInstance = getStorage();
  if (!storageInstance) {
    return null;
  }

  try {
    return storageInstance.getItem(key);
  } catch (error) {
    logger.error('LocalStorage get failed', 'storage.safeGetItem', error);
    return null;
  }
};

export const safeSetItem = (key: string, value: string): void => {
  const storageInstance = getStorage();
  if (!storageInstance) {
    return;
  }

  try {
    storageInstance.setItem(key, value);
  } catch (error) {
    logger.error('LocalStorage set failed', 'storage.safeSetItem', error);
  }
};

const safeRemoveItem = (key: string): void => {
  const storageInstance = getStorage();
  if (!storageInstance) {
    return;
  }

  try {
    storageInstance.removeItem(key);
  } catch (error) {
    logger.error('LocalStorage remove failed', 'storage.safeRemoveItem', error);
  }
};

// Kept in memory too, so a tab without writable storage stays signed in.
let cachedToken: string | null = null;
let tokenCacheInitialized = false;
const signOutListeners = new Set<() => void>();

export const authStorage = {
  getToken: (): string | null => {
    if (!tokenCacheInitialized) {
      cachedToken = safeGetItem(AUTH_TOKEN_KEY);
      tokenCacheInitialized = true;
    }
    return cachedToken;
  },
  setToken: (token: string): void => {
    cachedToken = token;
    tokenCacheInitialized = true;
    safeSetItem(AUTH_TOKEN_KEY, token);
  },
  clearAuth: (): void => {
    cachedToken = null;
    tokenCacheInitialized = true;
    safeRemoveItem(AUTH_TOKEN_KEY);
  },
  // Fires when another tab signs out (token removed). Not fired for this tab's own clearAuth.
  onRemoteSignOut: (listener: () => void): (() => void) => {
    signOutListeners.add(listener);
    return () => signOutListeners.delete(listener);
  },
};

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    // key === null is localStorage.clear() in another tab.
    if (event.key !== null && event.key !== AUTH_TOKEN_KEY) return;
    const wasSignedIn = cachedToken !== null;
    cachedToken = event.key === null ? null : event.newValue;
    tokenCacheInitialized = true;
    if (wasSignedIn && cachedToken === null) {
      signOutListeners.forEach((listener) => listener());
    }
  });
}

// Per-chat SSE last-seq for resume after refresh; pruned to bound storage.
export const chatStorage = {
  getEventId: (chatId: string): string | null =>
    safeGetItem(`${CHAT_EVENT_ID_PREFIX}${chatId}${CHAT_EVENT_ID_SUFFIX}`),
  setEventId: (chatId: string, eventId: string): void =>
    safeSetItem(`${CHAT_EVENT_ID_PREFIX}${chatId}${CHAT_EVENT_ID_SUFFIX}`, eventId),
  removeEventId: (chatId: string): void =>
    safeRemoveItem(`${CHAT_EVENT_ID_PREFIX}${chatId}${CHAT_EVENT_ID_SUFFIX}`),
  pruneStaleEntries: (): void => {
    const storage = getStorage();
    if (!storage) return;

    const entries: { key: string; seq: number }[] = [];
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (key?.startsWith(CHAT_EVENT_ID_PREFIX) && key.endsWith(CHAT_EVENT_ID_SUFFIX)) {
        const val = storage.getItem(key);
        entries.push({ key, seq: Number(val) || 0 });
      }
    }

    if (entries.length <= MAX_CHAT_EVENT_ID_ENTRIES) return;

    entries.sort((a, b) => b.seq - a.seq);
    for (let i = MAX_CHAT_EVENT_ID_ENTRIES; i < entries.length; i++) {
      storage.removeItem(entries[i].key);
    }
  },
};
