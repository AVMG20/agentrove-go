import { logger } from '@/utils/logger';

const AUTH_TOKEN_KEY = 'auth_token';
const REFRESH_TOKEN_KEY = 'refresh_token';
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

let cachedToken: string | null = null;
let cachedRefreshToken: string | null = null;
let tokenCacheInitialized = false;
const signOutListeners = new Set<() => void>();

function initTokenCacheFromLocalStorage(): void {
  if (tokenCacheInitialized) return;
  cachedToken = safeGetItem(AUTH_TOKEN_KEY);
  cachedRefreshToken = safeGetItem(REFRESH_TOKEN_KEY);
  tokenCacheInitialized = true;
}

// In-memory cache for sync getToken() on the API hot path, backed by localStorage.
export const authStorage = {
  getToken: (): string | null => {
    initTokenCacheFromLocalStorage();
    return cachedToken;
  },
  getRefreshToken: (): string | null => {
    initTokenCacheFromLocalStorage();
    return cachedRefreshToken;
  },
  setTokens: async (token: string, refreshToken: string): Promise<void> => {
    cachedToken = token;
    cachedRefreshToken = refreshToken;
    tokenCacheInitialized = true;
    safeSetItem(AUTH_TOKEN_KEY, token);
    safeSetItem(REFRESH_TOKEN_KEY, refreshToken);
  },
  clearAuth: (): void => {
    cachedToken = null;
    cachedRefreshToken = null;
    tokenCacheInitialized = true;
    safeRemoveItem(AUTH_TOKEN_KEY);
    safeRemoveItem(REFRESH_TOKEN_KEY);
  },
  // Storage events never reach a frozen, discarded or bfcached page, so its cache can
  // hold a refresh token another tab already rotated away. Call before using it.
  // Adopts only a present refresh token: a null here must not wipe tokens held in memory
  // when storage is unwritable (a real remote sign-out arrives via the storage event).
  syncFromStorage: (): void => {
    initTokenCacheFromLocalStorage();
    const storedRefresh = safeGetItem(REFRESH_TOKEN_KEY);
    if (!storedRefresh || storedRefresh === cachedRefreshToken) return;
    cachedRefreshToken = storedRefresh;
    cachedToken = safeGetItem(AUTH_TOKEN_KEY) ?? cachedToken;
  },
  // Fires when another tab signs out (tokens removed). Not fired for this tab's own clearAuth.
  onRemoteSignOut: (listener: () => void): (() => void) => {
    signOutListeners.add(listener);
    return () => signOutListeners.delete(listener);
  },
};

// Cross-tab: another tab rotating tokens must update this tab's memory cache
// or the next refresh replays a rotated-away token after the grace window.
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    // key === null is localStorage.clear() in another tab.
    const wasSignedIn = cachedToken !== null || cachedRefreshToken !== null;
    if (event.key === null) {
      cachedToken = null;
      cachedRefreshToken = null;
    } else if (event.key === AUTH_TOKEN_KEY) {
      cachedToken = event.newValue;
    } else if (event.key === REFRESH_TOKEN_KEY) {
      cachedRefreshToken = event.newValue;
    } else {
      return;
    }
    if (wasSignedIn && cachedToken === null && cachedRefreshToken === null) {
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
