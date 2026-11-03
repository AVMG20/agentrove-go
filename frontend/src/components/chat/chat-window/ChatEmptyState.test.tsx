// @vitest-environment happy-dom

import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { ChatEmptyState } from './ChatEmptyState';

afterEach(cleanup);

describe('ChatEmptyState', () => {
  it('fills the composer with a starter instead of sending it', () => {
    const onPickStarter = vi.fn();
    render(<ChatEmptyState onPickStarter={onPickStarter} />);

    expect(screen.getByRole('heading', { name: 'Start the conversation' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Find and fix a bug' }));

    expect(onPickStarter).toHaveBeenCalledWith('Find and fix a bug');
  });
});
