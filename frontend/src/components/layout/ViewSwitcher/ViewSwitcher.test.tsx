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

let artifacts: { id: string; updated_at: string }[] | undefined = [];
vi.mock('@/hooks/queries/useArtifactQueries', () => ({
  useChatArtifactsQuery: () => ({ data: artifacts }),
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
  artifacts = [];
  useUIStore.setState({ artifactSeenByChat: {}, visibleLayout: [['agent:primary']] });
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

describe('ViewSwitcher artifact button', () => {
  it('stays hidden until the chat has an artifact', () => {
    renderSwitcher();
    expect(screen.queryByRole('button', { name: /artifact view/ })).toBeNull();
  });

  it('opens the chat artifact beside it', () => {
    artifacts = [{ id: 'a1', updated_at: '2026-10-28T10:00:00Z' }];
    const toggleArtifact = vi.fn();
    vi.spyOn(useUIStore, 'getState').mockReturnValue({
      ...useUIStore.getState(),
      toggleArtifact,
    });
    renderSwitcher();
    fireEvent.click(screen.getByRole('button', { name: 'Toggle artifact view' }));
    expect(toggleArtifact).toHaveBeenCalledWith('c1', null);
  });

  it('marks a version published after load as unseen', () => {
    artifacts = [{ id: 'a1', updated_at: '2026-10-28T10:00:00Z' }];
    useUIStore.setState({ artifactSeenByChat: { c1: '2026-10-28T09:00:00Z' } });
    renderSwitcher();
    expect(screen.getByRole('button', { name: 'Toggle artifact view (updated)' })).toBeTruthy();
  });

  it('counts what was there on first load as seen', () => {
    artifacts = [{ id: 'a1', updated_at: '2026-10-28T10:00:00Z' }];
    renderSwitcher();
    expect(screen.getByRole('button', { name: 'Toggle artifact view' })).toBeTruthy();
    expect(useUIStore.getState().artifactSeenByChat.c1).toBe('2026-10-28T10:00:00Z');
  });
});
