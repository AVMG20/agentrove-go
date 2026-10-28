// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('@/utils/logger', () => ({ logger: { error: vi.fn() } }));

// Fresh module per test so the in-memory token cache resets between cases.
async function load() {
  vi.resetModules();
  return import('./storage');
}

beforeEach(() => {
  localStorage.clear();
});

describe('safe localStorage helpers', () => {
  it('round-trips a value and returns null for a missing key', async () => {
    const { safeSetItem, safeGetItem } = await load();
    safeSetItem('k', 'v');
    expect(safeGetItem('k')).toBe('v');
    expect(safeGetItem('absent')).toBeNull();
  });

  it('returns null instead of throwing when getItem blows up', async () => {
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('quota');
    });
    const { safeGetItem } = await load();
    expect(safeGetItem('k')).toBeNull();
    spy.mockRestore();
  });
});

describe('authStorage', () => {
  it('persists and reads back the token', async () => {
    const { authStorage } = await load();
    authStorage.setToken('tok');
    expect(authStorage.getToken()).toBe('tok');
    expect(localStorage.getItem('auth_token')).toBe('tok');
  });

  it('reads a token stored by an earlier page load', async () => {
    localStorage.setItem('auth_token', 'existing');
    const { authStorage } = await load();
    expect(authStorage.getToken()).toBe('existing');
  });

  it('clears the token from memory and storage', async () => {
    const { authStorage } = await load();
    authStorage.setToken('a');
    authStorage.clearAuth();
    expect(authStorage.getToken()).toBeNull();
    expect(localStorage.getItem('auth_token')).toBeNull();
  });

  it('adopts a token another tab stored', async () => {
    const { authStorage } = await load();
    authStorage.setToken('tok');
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', newValue: 'renewed' }));
    expect(authStorage.getToken()).toBe('renewed');
  });

  it('notifies listeners when another tab signs out', async () => {
    const { authStorage } = await load();
    authStorage.setToken('tok');
    const listener = vi.fn();
    const unsubscribe = authStorage.onRemoteSignOut(listener);
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', newValue: null }));
    expect(listener).toHaveBeenCalledTimes(1);
    expect(authStorage.getToken()).toBeNull();
    unsubscribe();
  });

  it('treats localStorage.clear() in another tab as a sign-out', async () => {
    const { authStorage } = await load();
    authStorage.setToken('tok');
    const listener = vi.fn();
    authStorage.onRemoteSignOut(listener);
    window.dispatchEvent(new StorageEvent('storage', { key: null }));
    expect(listener).toHaveBeenCalledTimes(1);
  });

  it('does not notify when already signed out or for unrelated keys', async () => {
    const { authStorage } = await load();
    const listener = vi.fn();
    authStorage.onRemoteSignOut(listener);
    window.dispatchEvent(new StorageEvent('storage', { key: 'auth_token', newValue: null }));
    authStorage.setToken('tok');
    window.dispatchEvent(new StorageEvent('storage', { key: 'other', newValue: null }));
    expect(listener).not.toHaveBeenCalled();
    expect(authStorage.getToken()).toBe('tok');
  });
});

describe('chatStorage event ids', () => {
  it('scopes event ids per chat under the chat: key namespace', async () => {
    const { chatStorage } = await load();
    chatStorage.setEventId('chat-a', '42');
    expect(chatStorage.getEventId('chat-a')).toBe('42');
    expect(localStorage.getItem('chat:chat-a:lastEventId')).toBe('42');
    expect(chatStorage.getEventId('chat-b')).toBeNull();

    chatStorage.removeEventId('chat-a');
    expect(chatStorage.getEventId('chat-a')).toBeNull();
  });

  it('prunes to the newest 500 entries, evicting the lowest seqs', async () => {
    // 505 entries; keys carry the seq as their stored value.
    for (let seq = 0; seq < 505; seq++) {
      localStorage.setItem(`chat:c${seq}:lastEventId`, String(seq));
    }
    const { chatStorage } = await load();
    chatStorage.pruneStaleEntries();

    const remaining = Object.keys(localStorage).filter(
      (k) => k.startsWith('chat:') && k.endsWith(':lastEventId'),
    );
    expect(remaining).toHaveLength(500);
    // The 5 lowest seqs (0..4) are evicted; the newest survive.
    expect(chatStorage.getEventId('c0')).toBeNull();
    expect(chatStorage.getEventId('c4')).toBeNull();
    expect(chatStorage.getEventId('c5')).toBe('5');
    expect(chatStorage.getEventId('c504')).toBe('504');
  });

  it('leaves entries untouched when under the cap', async () => {
    const { chatStorage } = await load();
    chatStorage.setEventId('only', '1');
    chatStorage.pruneStaleEntries();
    expect(chatStorage.getEventId('only')).toBe('1');
  });
});
