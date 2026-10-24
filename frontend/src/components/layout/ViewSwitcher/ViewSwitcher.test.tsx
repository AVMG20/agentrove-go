// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { useUIStore } from '@/store/uiStore';
import { ViewSwitcher } from './ViewSwitcher';

let browserAvailable: boolean | undefined = true;
vi.mock('@/hooks/queries/useSettingsQueries', () => ({
  useSettingsQuery: () => ({
    data: browserAvailable === undefined ? undefined : { browser_available: browserAvailable },
  }),
}));

function renderSwitcher() {
  return render(
    <MemoryRouter initialEntries={['/chat/c1']}>
      <ViewSwitcher />
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  browserAvailable = true;
  vi.restoreAllMocks();
  window.innerWidth = 1024;
});

describe('ViewSwitcher browser button', () => {
  it('toggles the browser view like the other views', () => {
    const toggleView = vi.fn();
    vi.spyOn(useUIStore, 'getState').mockReturnValue({
      ...useUIStore.getState(),
      toggleView,
    });
    renderSwitcher();
    fireEvent.click(screen.getByRole('button', { name: 'Toggle Browser view' }));
    expect(toggleView).toHaveBeenCalledWith('browser', true);
  });

  it('is shown while settings load', () => {
    browserAvailable = undefined;
    renderSwitcher();
    expect(screen.getByRole('button', { name: 'Toggle Browser view' })).toBeTruthy();
  });

  it('is hidden when the server runs no box browsers', () => {
    browserAvailable = false;
    renderSwitcher();
    expect(screen.queryByRole('button', { name: 'Toggle Browser view' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Toggle Terminal view' })).toBeTruthy();
  });
});

describe('ViewSwitcher hints', () => {
  it('shows shortcut and split hints on desktop', () => {
    renderSwitcher();
    const hints = screen.getAllByRole('tooltip').map((el) => el.textContent);
    expect(hints.some((hint) => hint?.includes('right-click to split'))).toBe(true);
  });

  it('drops them on a phone, where a tap would leave them stuck open', () => {
    window.innerWidth = 390;
    renderSwitcher();
    expect(screen.queryAllByRole('tooltip')).toEqual([]);
    expect(screen.getByRole('button', { name: 'Toggle Terminal view' })).toBeTruthy();
  });
});
