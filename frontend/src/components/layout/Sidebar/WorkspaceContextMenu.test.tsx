// @vitest-environment happy-dom

import { createRef } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { WorkspaceContextMenu } from './WorkspaceContextMenu';

afterEach(cleanup);

function renderMenu(appearance = { icon: null as string | null, color: null as string | null }) {
  const onChangeAppearance = vi.fn();
  render(
    <WorkspaceContextMenu
      ref={createRef<HTMLDivElement>()}
      position={{ top: 10, left: 10 }}
      appearance={appearance}
      onNewThread={vi.fn()}
      onRename={vi.fn()}
      onChangeAppearance={onChangeAppearance}
      onDelete={vi.fn()}
    />,
  );
  return { onChangeAppearance };
}

describe('WorkspaceContextMenu', () => {
  it('opens the icon and color picker in place and goes back', () => {
    renderMenu();
    fireEvent.click(screen.getByRole('menuitem', { name: /Icon & color/ }));

    expect(screen.getByRole('dialog', { name: 'Project icon and color' })).toBeTruthy();
    expect(screen.queryByRole('menuitem')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Back to project menu' }));
    expect(screen.getByRole('menuitem', { name: 'Rename project' })).toBeTruthy();
  });

  it('marks the current presets and reports picks, the defaults as null', () => {
    const { onChangeAppearance } = renderMenu({ icon: 'rocket', color: 'blue' });
    fireEvent.click(screen.getByRole('menuitem', { name: /Icon & color/ }));

    expect(screen.getByRole('button', { name: 'Rocket' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
    expect(screen.getByRole('button', { name: 'Folder' }).getAttribute('aria-pressed')).toBe(
      'false',
    );
    expect(screen.getByRole('button', { name: 'Blue' }).getAttribute('aria-pressed')).toBe('true');

    fireEvent.click(screen.getByRole('button', { name: 'Globe' }));
    fireEvent.click(screen.getByRole('button', { name: 'Folder' }));
    fireEvent.click(screen.getByRole('button', { name: 'Green' }));
    fireEvent.click(screen.getByRole('button', { name: 'Default' }));
    expect(onChangeAppearance.mock.calls).toEqual([
      [{ icon: 'globe' }],
      [{ icon: null }],
      [{ color: 'green' }],
      [{ color: null }],
    ]);
  });

  it('treats unknown stored presets as the defaults', () => {
    renderMenu({ icon: 'retired-icon', color: 'retired-color' });
    fireEvent.click(screen.getByRole('menuitem', { name: /Icon & color/ }));

    expect(screen.getByRole('button', { name: 'Folder' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
    expect(screen.getByRole('button', { name: 'Default' }).getAttribute('aria-pressed')).toBe(
      'true',
    );
  });
});
