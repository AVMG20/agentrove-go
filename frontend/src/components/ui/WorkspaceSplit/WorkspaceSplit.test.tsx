// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import type { TileId } from '@/types/ui.types';
import { WorkspaceSplit } from './WorkspaceSplit';

let WIDTH = 1000;

function mockSize() {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: WIDTH,
    bottom: 800,
    width: WIDTH,
    height: 800,
    toJSON: () => ({}),
  } as DOMRect);
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(WIDTH);
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockReturnValue(800);
}

beforeEach(() => {
  mockSize();
  globalThis.ResizeObserver ??= class {
    observe() {}
    disconnect() {}
    unobserve() {}
  } as unknown as typeof ResizeObserver;
});

afterEach(() => {
  WIDTH = 1000;
  cleanup();
  vi.restoreAllMocks();
});

function renderSplit(layout: TileId[][], paneSizes: Record<string, number[]> = {}) {
  const onResize = vi.fn();
  const tiles = layout.flat();
  const utils = render(
    <WorkspaceSplit
      openTabs={tiles}
      visibleLayout={layout}
      renderView={(tileId) => <div data-testid={tileId} />}
      paneSizes={paneSizes}
      onResize={onResize}
    />,
  );
  return { ...utils, onResize };
}

const widthOf = (tile: string) => screen.getByTestId(tile).parentElement!.style.width;

describe('WorkspaceSplit', () => {
  it('splits evenly without a saved size and uses the saved one when present', () => {
    renderSplit([['agent:primary', 'artifact']]);
    expect(widthOf('artifact')).toBe('50%');
    cleanup();
    renderSplit([['agent:primary', 'artifact']], { 'cols:agent:primary|artifact': [0.4, 0.6] });
    expect(widthOf('artifact')).toBe('60%');
  });

  it('lifts a saved pane that would be under the minimum on this screen', () => {
    renderSplit([['agent:primary', 'artifact']], { 'cols:agent:primary|artifact': [0.1, 0.9] });
    // 280px of 1000px.
    expect(parseFloat(widthOf('agent:primary'))).toBeCloseTo(28);
  });

  it('moves a divider with the arrow keys', () => {
    const { onResize } = renderSplit([['agent:primary', 'artifact']]);
    fireEvent.keyDown(screen.getByRole('separator'), { key: 'ArrowLeft' });
    const [key, sizes] = onResize.mock.calls[0];
    expect(key).toBe('cols:agent:primary|artifact');
    expect(sizes[0]).toBeCloseTo(0.48);
    expect(sizes[1]).toBeCloseTo(0.52);
  });

  it('still moves a divider between panes too narrow for the minimums', () => {
    // Three even panes on 800px: each pair is 533px, under twice the 280px minimum.
    WIDTH = 800;
    vi.restoreAllMocks();
    mockSize();
    const { onResize } = renderSplit([['agent:primary', 'diff', 'artifact']]);
    fireEvent.keyDown(screen.getAllByRole('separator')[0], { key: 'ArrowRight' });
    const sizes = onResize.mock.calls[0][1];
    expect(sizes[0]).toBeCloseTo(1 / 3 + 0.02);
  });

  it('saves the size on release and unlocks the panes', () => {
    const { onResize, container } = renderSplit([['agent:primary', 'artifact']]);
    const divider = screen.getByRole('separator');
    fireEvent.pointerDown(divider, { button: 0, clientX: 500, pointerId: 1 });
    expect(container.firstElementChild!.className).toMatch(/dragging/);
    act(() => {
      window.dispatchEvent(new MouseEvent('pointermove', { clientX: 600 }));
      window.dispatchEvent(new MouseEvent('pointerup', { clientX: 600 }));
    });
    expect(onResize).toHaveBeenCalledTimes(1);
    expect(onResize.mock.calls[0][1][0]).toBeCloseTo(0.6);
    expect(container.firstElementChild!.className).not.toMatch(/dragging/);
  });

  it('ends a drag whose divider disappears mid-drag', () => {
    const { onResize, container, rerender } = renderSplit([['agent:primary', 'artifact']]);
    fireEvent.pointerDown(screen.getByRole('separator'), { button: 0, clientX: 500, pointerId: 1 });
    rerender(
      <WorkspaceSplit
        openTabs={['agent:primary', 'artifact']}
        visibleLayout={[['agent:primary']]}
        renderView={(tileId) => <div data-testid={tileId} />}
        paneSizes={{}}
        onResize={onResize}
      />,
    );
    act(() => {
      window.dispatchEvent(new MouseEvent('pointerup', { clientX: 500 }));
    });
    expect(container.firstElementChild!.className).not.toMatch(/dragging/);
  });
});
