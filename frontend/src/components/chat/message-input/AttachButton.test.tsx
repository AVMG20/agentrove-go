// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { setViewport } from '@/test-utils/device';
import { AttachButton } from './AttachButton';

afterEach(() => {
  cleanup();
  setViewport(1024, 768);
  vi.restoreAllMocks();
});

const button = () => screen.getByRole('button', { name: 'Attach file' });
const fileInput = () => document.querySelector<HTMLInputElement>('input[type="file"]');

describe('AttachButton', () => {
  it('opens the system picker directly on a phone and hands over what was picked', () => {
    setViewport(390, 844);
    const onAttach = vi.fn();
    const onFiles = vi.fn();
    render(<AttachButton onAttach={onAttach} onFiles={onFiles} />);

    const input = fileInput()!;
    const click = vi.spyOn(input, 'click').mockImplementation(() => {});
    fireEvent.click(button());
    expect(click).toHaveBeenCalled();
    expect(onAttach).not.toHaveBeenCalled();

    const photo = new File(['x'], 'photo.png', { type: 'image/png' });
    fireEvent.change(input, { target: { files: [photo] } });
    expect(onFiles).toHaveBeenCalledWith([photo]);
  });

  it('opens the upload dialog on desktop', () => {
    const onAttach = vi.fn();
    render(<AttachButton onAttach={onAttach} onFiles={vi.fn()} />);
    expect(fileInput()).toBeNull();
    fireEvent.click(button());
    expect(onAttach).toHaveBeenCalled();
  });

  it('keeps the dialog on a phone when there is nowhere to hand files', () => {
    setViewport(390, 844);
    const onAttach = vi.fn();
    render(<AttachButton onAttach={onAttach} />);
    expect(fileInput()).toBeNull();
    fireEvent.click(button());
    expect(onAttach).toHaveBeenCalled();
  });
});
