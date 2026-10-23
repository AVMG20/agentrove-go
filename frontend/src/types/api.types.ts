import type { Chat, Message } from './chat.types';

export interface PaginationParams {
  page: number;
  per_page: number;
}

export interface CursorPaginationParams {
  cursor?: string;
  limit?: number;
}

export interface PaginatedResponse<T> {
  items: T[];
  page: number;
  per_page: number;
  total: number;
  pages: number;
}

export interface CursorPaginatedResponse<T> {
  items: T[];
  next_cursor: string | null;
  has_more: boolean;
}

export type PaginatedChats = PaginatedResponse<Chat>;
export type PaginatedMessages = CursorPaginatedResponse<Message>;
