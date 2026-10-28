import { authStorage } from '@/utils/storage';
import { invalidateSessionAndRedirect } from '@/utils/authSession';
import { NetworkError } from '@/types/errors.types';

type RequestMethod = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';

interface RequestOptions extends RequestInit {
  data?: unknown;
  formData?: FormData;
  signal?: AbortSignal;
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
  onSessionExpired: () => void;
}

const localAuth: ClientAuth = {
  getToken: () => authStorage.getToken(),
  onSessionExpired: invalidateSessionAndRedirect,
};

function tokenExpired(token: string): boolean {
  try {
    const base64 = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
    const { exp } = JSON.parse(atob(base64 + '='.repeat((4 - (base64.length % 4)) % 4)));
    return typeof exp === 'number' && exp * 1000 <= Date.now();
  } catch {
    return false;
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

  // For SSE and WebSocket openers that bypass request(). Null (and signed out) once the
  // token has expired, so they stop reconnecting.
  async getValidToken(): Promise<string | null> {
    const token = this.auth.getToken();
    if (token && tokenExpired(token)) {
      this.auth.onSessionExpired();
      return null;
    }
    return token;
  }

  private authHeaders(includeContentType = true): Record<string, string> {
    const token = this.auth.getToken();
    return {
      ...(includeContentType ? { 'Content-Type': 'application/json' } : {}),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    };
  }

  // Only a missing, expired or signed-out session gets a 401 from the API.
  private sessionExpired(): never {
    this.auth.onSessionExpired();
    throw new Error('Session expired');
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

    if (response.status === 401 && endpoint !== '/auth/jwt/login') {
      this.sessionExpired();
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

  async getBlob(endpoint: string, signal?: AbortSignal): Promise<Blob> {
    const response = await fetch(`${this.baseURL}${endpoint}`, {
      method: 'GET',
      headers: this.authHeaders(false),
      signal,
    });

    if (response.status === 401) {
      this.sessionExpired();
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
