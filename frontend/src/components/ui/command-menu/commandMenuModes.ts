import { MAIN_FILTERS, type MainFilter, type MenuMode } from './commandRegistry';

export const FILTER_LABELS: Record<MainFilter, string> = {
  all: 'All',
  chats: 'Chats',
  messages: 'Messages',
  settings: 'Settings',
};

export const isMainMode = (mode: MenuMode): mode is MainFilter =>
  (MAIN_FILTERS as MenuMode[]).includes(mode);
