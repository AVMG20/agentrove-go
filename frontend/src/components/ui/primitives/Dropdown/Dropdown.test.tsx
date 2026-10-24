// @vitest-environment jsdom

import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { BaseModal } from '@/components/ui/shared/BaseModal/BaseModal';
import { setViewport } from '@/test-utils/device';
import { Dropdown } from './Dropdown';

const MODELS = ['Opus', 'Sonnet', 'Haiku'];

function Picker({ onSelect = vi.fn() }: { onSelect?: (m: string) => void }) {
  const [value, setValue] = useState('Opus');
  return (
    <Dropdown
      value={value}
      items={MODELS}
      getItemKey={(m) => m}
      getItemLabel={(m) => m}
      onSelect={(m) => {
        setValue(m);
        onSelect(m);
      }}
      searchable
    />
  );
}

function InDialog({ onClose = vi.fn() }: { onClose?: () => void }) {
  return (
    <BaseModal isOpen onClose={onClose} ariaLabel="Edit automation">
      <Picker />
    </BaseModal>
  );
}

const open = () => fireEvent.click(screen.getByRole('button', { name: /Opus/ }));
const listbox = () => screen.queryByRole('listbox');

afterEach(() => {
  cleanup();
  setViewport(1024, 768);
});

describe('Dropdown on a phone', () => {
  it('opens as a sheet portalled to <body>, outside any dialog', () => {
    setViewport(390, 844);
    render(<InDialog />);
    open();
    const list = listbox()!;
    // Not inside the dialog's (scrolling, blurred) backdrop, which would become
    // its containing block and move it.
    expect(list.closest('[role="presentation"]')).toBeNull();
    expect(list.closest('[role="dialog"]')).toBeNull();
    expect(list.parentElement?.parentElement).toBe(document.body);
  });

  it('keeps taps inside the sheet from closing it, and selects', () => {
    setViewport(390, 844);
    const onSelect = vi.fn();
    render(<Picker onSelect={onSelect} />);
    open();
    fireEvent.mouseDown(screen.getByPlaceholderText('Search...'));
    expect(listbox()).not.toBeNull();
    fireEvent.click(screen.getByRole('option', { name: /Sonnet/ }));
    expect(onSelect).toHaveBeenCalledWith('Sonnet');
    expect(listbox()).toBeNull();
  });

  it('closes on a tap on its scrim', () => {
    setViewport(390, 844);
    render(<Picker />);
    open();
    const scrim = listbox()!.previousElementSibling!;
    fireEvent.mouseDown(scrim);
    fireEvent.click(scrim);
    expect(listbox()).toBeNull();
  });

  it('takes Escape for itself, leaving the dialog open', () => {
    setViewport(390, 844);
    const onClose = vi.fn();
    render(<InDialog onClose={onClose} />);
    open();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(listbox()).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('Dropdown on desktop', () => {
  it('stays an anchored popover inside the dropdown', () => {
    render(<InDialog />);
    open();
    expect(listbox()!.closest('[role="dialog"]')).not.toBeNull();
  });
});
