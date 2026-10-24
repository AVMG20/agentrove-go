// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { MobileChatTitle } from './ChatTabs';

vi.mock('@/hooks/queries/useChatQueries', () => ({
  useChatQuery: (chatId: string | undefined) => ({
    data: chatId ? { id: chatId, title: '**Fix** the login bug' } : undefined,
  }),
}));
vi.mock('@/hooks/useBlockedChatIds', () => ({ useBlockedChatIds: () => new Set(['c1']) }));
vi.mock('@/hooks/useWorkingChatIds', () => ({ useWorkingChatIds: () => new Set<string>() }));

afterEach(() => {
  cleanup();
  useUIStore.setState({ sessionSheetOpen: false });
});

describe('MobileChatTitle', () => {
  it('shows the thread title and status and opens the session sheet', () => {
    render(
      <MemoryRouter initialEntries={['/chat/c1']}>
        <MobileChatTitle />
      </MemoryRouter>,
    );
    const button = screen.getByRole('button', { name: /Fix the login bug · Needs you/ });
    expect(button.textContent).toContain('Fix the login bug');
    fireEvent.click(button);
    expect(useUIStore.getState().sessionSheetOpen).toBe(true);
  });
});
