// @vitest-environment jsdom

import { cleanup, render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { MarkDown } from './MarkDown';

describe('MarkDown math', () => {
  afterEach(cleanup);

  it('renders plain text without loading KaTeX', () => {
    const { container } = render(<MarkDown content="costs $5 and $10" />);
    expect(container.textContent).toContain('costs $5 and $10');
    expect(container.querySelector('.katex')).toBeNull();
  });

  it('lazy-loads KaTeX and renders display math once it arrives', async () => {
    const { container } = render(<MarkDown content={'$$x^2$$'} />);
    await waitFor(() => expect(container.querySelector('.katex')).not.toBeNull());
  });
});
