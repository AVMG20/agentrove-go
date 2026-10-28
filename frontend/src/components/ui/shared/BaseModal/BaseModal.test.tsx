// @vitest-environment happy-dom

import { useState } from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { BaseModal } from './BaseModal';

function Harness({ onClose = vi.fn() }: { onClose?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Open</button>
      <BaseModal
        isOpen={open}
        onClose={() => {
          onClose();
          setOpen(false);
        }}
        ariaLabelledBy="modal-title"
      >
        <h2 id="modal-title">Settings</h2>
        <button>First</button>
        <button>Last</button>
      </BaseModal>
    </>
  );
}

describe('BaseModal', () => {
  afterEach(cleanup);

  it('is a named modal dialog and moves focus inside on open', () => {
    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Open' });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = screen.getByRole('dialog', { name: 'Settings' });
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'First' }));
  });

  it('closes once on Escape, even when focus is outside the dialog', () => {
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));

    (document.activeElement as HTMLElement).blur();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('fires onClose once for Escape pressed inside the dialog', () => {
    const onClose = vi.fn();
    render(
      <BaseModal isOpen onClose={onClose} ariaLabel="Plain">
        <button>Only</button>
      </BaseModal>,
    );
    fireEvent.keyDown(screen.getByRole('button', { name: 'Only' }), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('dialog', { name: 'Plain' })).toBeTruthy();
  });

  it('restores focus to the previously focused element on close', () => {
    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Open' });
    trigger.focus();
    fireEvent.click(trigger);
    expect(document.activeElement).not.toBe(trigger);

    fireEvent.keyDown(document.activeElement as HTMLElement, { key: 'Escape' });
    expect(document.activeElement).toBe(trigger);
  });

  it('traps Tab and Shift+Tab inside the dialog', () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Open' }));
    const first = screen.getByRole('button', { name: 'First' });
    const last = screen.getByRole('button', { name: 'Last' });

    last.focus();
    fireEvent.keyDown(last, { key: 'Tab' });
    expect(document.activeElement).toBe(first);

    fireEvent.keyDown(first, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it('only the top-most modal handles Escape', () => {
    const outer = vi.fn();
    const inner = vi.fn();
    render(
      <>
        <BaseModal isOpen onClose={outer} ariaLabel="Outer">
          <button>Outer button</button>
        </BaseModal>
        <BaseModal isOpen onClose={inner} ariaLabel="Inner">
          <button>Inner button</button>
        </BaseModal>
      </>,
    );
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();
  });
});
