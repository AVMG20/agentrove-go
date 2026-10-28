// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { ChatTitle } from './ChatTitle';

const { mutate, mobile } = vi.hoisted(() => ({ mutate: vi.fn(), mobile: { value: true } }));

vi.mock('@/hooks/queries/useChatQueries', () => ({
  useChatQuery: (chatId: string | undefined) => ({
    data: chatId ? { id: chatId, title: '**Fix** the login bug' } : undefined,
  }),
  useUpdateChatMutation: () => ({ mutate }),
}));
vi.mock('@/hooks/useBlockedChatIds', () => ({ useBlockedChatIds: () => new Set(['c1']) }));
vi.mock('@/hooks/useWorkingChatIds', () => ({ useWorkingChatIds: () => new Set<string>() }));
vi.mock('@/hooks/useIsMobile', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/hooks/useIsMobile')>()),
  useIsMobile: () => mobile.value,
}));

afterEach(() => {
  cleanup();
  useUIStore.setState({ sessionSheetOpen: false });
  mutate.mockReset();
  mobile.value = true;
});

describe('ChatTitle', () => {
  it('shows the thread title and status and opens the session sheet on phones', () => {
    render(
      <MemoryRouter initialEntries={['/chat/c1']}>
        <ChatTitle />
      </MemoryRouter>,
    );
    const button = screen.getByRole('button', { name: /Fix the login bug · Needs you/ });
    expect(button.textContent).toContain('Fix the login bug');
    fireEvent.click(button);
    expect(useUIStore.getState().sessionSheetOpen).toBe(true);
  });

  it('renames in place on double click on desktop', () => {
    mobile.value = false;
    render(
      <MemoryRouter initialEntries={['/chat/c1']}>
        <ChatTitle />
      </MemoryRouter>,
    );
    fireEvent.doubleClick(screen.getByRole('heading'));
    const input = screen.getByLabelText('Thread title') as HTMLInputElement;
    expect(input.value).toBe('Fix the login bug');

    // Escape cancels without saving.
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.queryByLabelText('Thread title')).toBeNull();
    expect(mutate).not.toHaveBeenCalled();

    fireEvent.doubleClick(screen.getByRole('heading'));
    const again = screen.getByLabelText('Thread title');
    fireEvent.change(again, { target: { value: '  Login fix ' } });
    fireEvent.blur(again);
    expect(mutate).toHaveBeenCalledWith(
      { chatId: 'c1', updateData: { title: 'Login fix' } },
      expect.anything(),
    );
  });
});
