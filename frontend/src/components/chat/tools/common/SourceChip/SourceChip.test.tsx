// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, it, expect } from 'vitest';
import { SourceChip } from './SourceChip';

describe('SourceChip', () => {
  it('links http(s) sources in a new tab without the opener', () => {
    render(<SourceChip source={{ title: 'Docs', url: 'https://example.com/a' }} index={0} />);

    const link = screen.getByRole('link');
    expect(link.getAttribute('href')).toBe('https://example.com/a');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('does not render a javascript: url as a live href', () => {
    const { container } = render(
      <SourceChip source={{ title: 'Evil', url: 'javascript:alert(document.cookie)' }} index={0} />,
    );

    const anchor = container.querySelector('a');
    expect(anchor).not.toBeNull();
    expect(anchor?.hasAttribute('href')).toBe(false);
  });
});
