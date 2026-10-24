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
