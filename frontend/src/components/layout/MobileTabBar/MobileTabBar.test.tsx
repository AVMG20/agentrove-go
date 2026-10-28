// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { useUIStore } from '@/store/uiStore';
import { MobileTabBar } from './MobileTabBar';

let browserAvailable: boolean | undefined = true;
vi.mock('@/hooks/queries/useSettingsQueries', () => ({
  useSettingsQuery: () => ({
    data: browserAvailable === undefined ? undefined : { browser_available: browserAvailable },
  }),
}));

afterEach(() => {
  cleanup();
  browserAvailable = true;
  vi.restoreAllMocks();
  act(() => useUIStore.getState().resetWorkspace());
});

const tab = (name: string) => screen.getByRole('button', { name });

describe('MobileTabBar', () => {
  it('marks the view on screen, the chat by default', () => {
    render(<MobileTabBar />);
    expect(tab('Chat').getAttribute('aria-current')).toBe('page');
    expect(tab('Changes').getAttribute('aria-current')).toBeNull();

    act(() => useUIStore.setState({ visibleLayout: [['terminal']], focusedTile: 'terminal' }));
    expect(tab('Terminal').getAttribute('aria-current')).toBe('page');
    expect(tab('Chat').getAttribute('aria-current')).toBeNull();
  });

  it('switches views without toggling the current one off', () => {
    const toggleView = vi.fn();
    vi.spyOn(useUIStore, 'getState').mockReturnValue({ ...useUIStore.getState(), toggleView });
    render(<MobileTabBar />);
    fireEvent.click(tab('Files'));
    fireEvent.click(tab('Chat'));
    expect(toggleView.mock.calls).toEqual([
      ['editor', false],
      ['agent', false],
    ]);
  });

  it('leaves out the browser when the server runs no box browsers', () => {
    browserAvailable = false;
    render(<MobileTabBar />);
    expect(screen.queryByRole('button', { name: 'Browser' })).toBeNull();
    expect(screen.getAllByRole('button')).toHaveLength(4);
  });
});
