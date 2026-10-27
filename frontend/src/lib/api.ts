import { authStorage } from '@/utils/storage';
import { invalidateSessionAndRedirect } from '@/utils/authSession';
import { NetworkError } from '@/types/errors.types';

type RequestMethod = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

interface RequestOptions extends RequestInit {
  data?: unknown;
  formData?: FormData;
  signal?: AbortSignal;
}

interface TokenResponse {
  access_token: string;
  refresh_token: string;
  token_type: string;
}

class RefreshTokenError extends Error {
  status: number;

  constructor(status: number, message = 'Token refresh failed') {
    super(message);
    this.name = 'RefreshTokenError';
    this.status = status;
  }
}

export type { ApiStreamResponse as StreamResponse } from '@/types/stream.types';

export const trimTrailingSlash = (url: string): string => url.replace(/\/+$/, '');

const resolveHttpBaseUrl = (rawUrl: string): string =>
  trimTrailingSlash(new URL(rawUrl, window.location.origin).toString());

const resolveWsBaseUrl = (rawUrl: string): string => {
  const normalized = new URL(rawUrl, window.location.origin);
  normalized.protocol = ['https:', 'wss:'].includes(normalized.protocol) ? 'wss:' : 'ws:';
  return trimTrailingSlash(normalized.toString());
};

interface ClientAuth {
  getToken: () => string | null;
  getRefreshToken: () => string | null;
  setTokens: (accessToken: string, refreshToken: string) => Promise<void>;
  // Re-read tokens another tab may have rotated while this one missed the storage event.
  syncFromStorage: () => void;
  onSessionExpired: () => void;
}

const localAuth: ClientAuth = {
  getToken: () => authStorage.getToken(),
  getRefreshToken: () => authStorage.getRefreshToken(),
  setTokens: (accessToken, refreshToken) => authStorage.setTokens(accessToken, refreshToken),
  syncFromStorage: () => authStorage.syncFromStorage(),
  onSessionExpired: invalidateSessionAndRedirect,
};

// A stale refresh token (e.g. after a backend identity reset) gets a 401; that's terminal.
function shouldInvalidateSession(error: unknown): boolean {
  return error instanceof RefreshTokenError && error.status === 401;
}

const ACCESS_TOKEN_EXPIRY_BUFFER_MS = 30_000;

// How long to wait for another tab to store the token it just rotated (total ~1s).
const ROTATION_WAIT_MS = [100, 300, 600];

// Missing/undecodable claims or exp within buffer → don't open long-lived connections on it.
function accessTokenExpiresSoon(token: string): boolean {
  try {
    const base64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const claims = JSON.parse(atob(base64 + '='.repeat((4 - (base64.length % 4)) % 4)));
    if (typeof claims.exp !== 'number') return true;
    return claims.exp * 1000 - Date.now() < ACCESS_TOKEN_EXPIRY_BUFFER_MS;
  } catch {
    return true;
  }
}

const extractErrorMessage = async (response: Response): Promise<string> => {
  try {
    const text = await response.text();
    if (!text) {
      return `HTTP error! status: ${response.status}`;
    }
    const error = JSON.parse(text);
    return error.message || error.detail || response.statusText;
  } catch {
    return response.statusText || 'An error occurred';
  }
};

class APIClient {
  private baseURL: string;
  private auth: ClientAuth;
  private refreshingPromise: Promise<TokenResponse> | null = null;

  constructor(baseURL: string, auth: ClientAuth) {
    this.baseURL = baseURL;
    this.auth = auth;
  }

  getBaseUrl(): string {
    return this.baseURL;
  }

  // Prefer this over guessing which store pairs with the client (e.g. SSE query param).
  getToken(): string | null {
    return this.auth.getToken();
  }

  // For SSE openers that bypass request(): may need a refresh after restart / dead feed.
  async getValidToken(): Promise<string | null> {
    this.auth.syncFromStorage();
    const cached = this.auth.getToken();
    if (!this.auth.getRefreshToken()) return cached;
    // Don't rotate on every open — churns refresh tokens and can strand clients mid-rotation.
    if (cached && !accessTokenExpiresSoon(cached)) return cached;
    try {
      return (await this.refreshTokenIfNeeded()).access_token;
    } catch (error) {
      // Terminal refresh failure: tear down so SSE reopen loops don't retry forever.
      if (shouldInvalidateSession(error)) {
        this.auth.onSessionExpired();
        return null;
      }
      throw error;
    }
  }

  private authHeaders(includeContentType = true): Record<string, string> {
    const token = this.auth.getToken();
    return {
      ...(includeContentType ? { 'Content-Type': 'application/json' } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    };
  }

  private async performTokenRefresh(): Promise<TokenResponse> {
    for (;;) {
      // A stale refresh token (rotated by another tab beyond the server grace window) 401s
      // and would sign every tab out, so use the freshest one.
      this.auth.syncFromStorage();
      const refreshToken = this.auth.getRefreshToken();
      if (!refreshToken) {
        throw new RefreshTokenError(401, 'No refresh token available');
      }

      const response = await fetch(`${this.baseURL}/auth/jwt/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      }).catch(() => {
        throw new RefreshTokenError(0);
      });

      // Two tabs refreshing at once (e.g. after a wake): the loser's 401 can arrive before the
      // winner has stored its replacement, so wait for that instead of signing everyone out.
      if (response.status === 401 && (await this.awaitRotatedToken(refreshToken))) {
        continue;
      }

      if (!response.ok) {
        throw new RefreshTokenError(response.status);
      }

      const data: TokenResponse = await response.json();
      await this.auth.setTokens(data.access_token, data.refresh_token);
      return data;
    }
  }

  // True once another tab has stored a different refresh token than staleToken.
  private async awaitRotatedToken(staleToken: string): Promise<boolean> {
    for (const delayMs of ROTATION_WAIT_MS) {
      await new Promise((resolve) => setTimeout(resolve, delayMs));
      this.auth.syncFromStorage();
      if (this.auth.getRefreshToken() !== staleToken) return true;
    }
    return false;
  }

  private async refreshTokenIfNeeded(): Promise<TokenResponse> {
    if (this.refreshingPromise) {
      return this.refreshingPromise;
    }

    this.refreshingPromise = this.performTokenRefresh();

    try {
      return await this.refreshingPromise;
    } finally {
      this.refreshingPromise = null;
    }
  }

  private async handleResponse<T>(response: Response): Promise<T | null> {
    if (!response.ok) {
      const errorMessage = await extractErrorMessage(response);
      const error = new Error(errorMessage) as Error & { status?: number };
      error.status = response.status;
      throw error;
    }

    const text = await response.text();
    return text ? JSON.parse(text) : null;
  }

  private async request<T>(
    endpoint: string,
    method: RequestMethod = 'GET',
    options: RequestOptions = {},
    additionalHeaders: Record<string, string> = {},
    isRetry = false,
  ): Promise<T | null> {
    const { data, formData, signal, ...customConfig } = options;

    const config: RequestInit = {
      method,
      headers: {
        ...this.authHeaders(!formData),
        ...additionalHeaders,
      },
      signal,
      ...customConfig,
    };

    if (data) {
      config.body = JSON.stringify(data);
    }

    if (formData) {
      config.body = formData;
    }

    let response: Response;
    try {
      response = await fetch(`${this.baseURL}${endpoint}`, config);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        throw error;
      }
      throw new NetworkError();
    }

    if (response.status === 401 && !isRetry && !endpoint.includes('/auth/jwt/')) {
      const hasRefreshToken = !!this.auth.getRefreshToken();
      if (hasRefreshToken) {
        try {
          await this.refreshTokenIfNeeded();
          return this.request<T>(endpoint, method, options, additionalHeaders, true);
        } catch (error) {
          if (shouldInvalidateSession(error)) {
            this.auth.onSessionExpired();
            throw new Error('Session expired');
          }
          throw error;
        }
      }
      // 401 without refresh → session dead; don't leave UI looking connected.
      this.auth.onSessionExpired();
      throw new Error('Session expired');
    }

    return this.handleResponse(response);
  }

  async get<T>(endpoint: string, signal?: AbortSignal) {
    return this.request<T>(endpoint, 'GET', { signal });
  }

  async post<T>(endpoint: string, data?: unknown, signal?: AbortSignal) {
    return this.request<T>(endpoint, 'POST', { data, signal });
  }

  async patch<T>(endpoint: string, data?: unknown, signal?: AbortSignal) {
    return this.request<T>(endpoint, 'PATCH', { data, signal });
  }

  async put<T>(endpoint: string, data?: unknown, signal?: AbortSignal) {
    return this.request<T>(endpoint, 'PUT', { data, signal });
  }

  async postForm<T>(endpoint: string, formData: FormData, signal?: AbortSignal) {
    return this.request<T>(endpoint, 'POST', { formData, signal });
  }

  async delete(endpoint: string, signal?: AbortSignal) {
    return this.request(endpoint, 'DELETE', { signal });
  }

  async getBlob(endpoint: string, signal?: AbortSignal, isRetry = false): Promise<Blob> {
    const response = await fetch(`${this.baseURL}${endpoint}`, {
      method: 'GET',
      headers: this.authHeaders(false),
      signal,
    });

    if (response.status === 401 && !isRetry) {
      const hasRefreshToken = !!this.auth.getRefreshToken();
      if (hasRefreshToken) {
        try {
          await this.refreshTokenIfNeeded();
          return this.getBlob(endpoint, signal, true);
        } catch (error) {
          if (shouldInvalidateSession(error)) {
            this.auth.onSessionExpired();
            throw new Error('Session expired');
          }
          throw error;
        }
      }
    }

    if (!response.ok) {
      const errorMessage = await extractErrorMessage(response);
      throw new Error(errorMessage);
    }

    return response.blob();
  }
}

const API_BASE_URL: string = resolveHttpBaseUrl(import.meta.env.VITE_API_BASE_URL || '/api/v1');
export const WS_BASE_URL: string = resolveWsBaseUrl(import.meta.env.VITE_WS_URL || '/api/v1/ws');
// The API prefix as a ws(s) URL, for sockets under regular API routes.
export const API_WS_URL: string = resolveWsBaseUrl(import.meta.env.VITE_API_BASE_URL || '/api/v1');

export const apiClient = new APIClient(API_BASE_URL, localAuth);
