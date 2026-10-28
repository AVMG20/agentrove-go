import { useMemo, useRef, useState, ReactNode } from 'react';
import type { KeyboardEvent, PointerEvent as ReactPointerEvent } from 'react';
import clsx from 'clsx';
import type { TileId } from '@/types/ui.types';
import styles from './WorkspaceSplit.module.scss';

// Narrowest a pane can be dragged (unless the pair is narrower to begin with).
const MIN_PANE_WIDTH = 280;
const MIN_PANE_HEIGHT = 160;
// Arrow keys on a focused divider move it by this fraction.
const KEY_STEP = 0.02;

interface WorkspaceSplitProps {
  // Every open tab is mounted so background panes keep state.
  openTabs: TileId[];
  visibleLayout: TileId[][];
  // isVisible false for mounted-but-hidden tabs (skip terminal fit/focus, etc.).
  renderView: (tileId: TileId, isVisible: boolean) => ReactNode;
  // Saved fractions by sizeKey; missing or stale entries split evenly.
  paneSizes: Record<string, number[]>;
  onResize: (key: string, sizes: number[]) => void;
}

interface TilePlacement {
  top: number;
  height: number;
  left: number;
  width: number;
  topBorder: boolean;
  leftBorder: boolean;
}

// A divider between panes `index` and `index + 1` of one sized group: the
// columns of a row, or the rows themselves.
interface Divider {
  axis: 'x' | 'y';
  key: string;
  index: number;
  sizes: number[];
  // Position of the divider, and the span it covers on the other axis.
  at: number;
  spanStart: number;
  spanSize: number;
}

// Keyed by the tiles involved, so each pairing remembers its own split.
const rowKey = (layout: TileId[][]) => `rows:${layout.map((row) => row.join('|')).join('/')}`;
const columnKey = (row: TileId[]) => `cols:${row.join('|')}`;

function sizesOrEven(saved: number[] | undefined, count: number): number[] {
  if (saved?.length === count && saved.every((size) => size > 0)) return saved;
  return Array.from({ length: count }, () => 1 / count);
}

const offsets = (sizes: number[]) =>
  sizes.map((_, i) => sizes.slice(0, i).reduce((a, b) => a + b, 0));

const pct = (fraction: number) => `${fraction * 100}%`;

// Tiles are absolutely placed in one flat list (DOM order = openTabs), so moving
// or resizing a pane never remounts it — terminals and artifact pages keep state.
export function WorkspaceSplit({
  openTabs,
  visibleLayout,
  renderView,
  paneSizes,
  onResize,
}: WorkspaceSplitProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  // Live sizes while a divider is dragged; saved on release.
  const [draft, setDraft] = useState<{ key: string; sizes: number[] } | null>(null);
  const [dragAxis, setDragAxis] = useState<'x' | 'y' | null>(null);

  const { placements, dividers } = useMemo(() => {
    const sizesFor = (key: string, count: number) =>
      draft?.key === key ? draft.sizes : sizesOrEven(paneSizes[key], count);
    const map = new Map<TileId, TilePlacement>();
    const lines: Divider[] = [];
    const rKey = rowKey(visibleLayout);
    const rowSizes = sizesFor(rKey, visibleLayout.length);
    const rowTops = offsets(rowSizes);
    visibleLayout.forEach((row, r) => {
      const cKey = columnKey(row);
      const colSizes = sizesFor(cKey, row.length);
      const colLefts = offsets(colSizes);
      row.forEach((tileId, c) => {
        map.set(tileId, {
          top: rowTops[r],
          height: rowSizes[r],
          left: colLefts[c],
          width: colSizes[c],
          topBorder: r > 0,
          leftBorder: c > 0,
        });
        if (c > 0) {
          lines.push({
            axis: 'x',
            key: cKey,
            index: c - 1,
            sizes: colSizes,
            at: colLefts[c],
            spanStart: rowTops[r],
            spanSize: rowSizes[r],
          });
        }
      });
      if (r > 0) {
        lines.push({
          axis: 'y',
          key: rKey,
          index: r - 1,
          sizes: rowSizes,
          at: rowTops[r],
          spanStart: 0,
          spanSize: 1,
        });
      }
    });
    return { placements: map, dividers: lines };
  }, [visibleLayout, paneSizes, draft]);

  // Moves the divider to `fraction` of the pair's start, kept off the minimums.
  const resized = (divider: Divider, delta: number, total: number) => {
    const { index, sizes } = divider;
    const pair = sizes[index] + sizes[index + 1];
    const minPx = divider.axis === 'x' ? MIN_PANE_WIDTH : MIN_PANE_HEIGHT;
    const min = Math.min(minPx / total, pair / 2);
    const first = Math.min(pair - min, Math.max(min, sizes[index] + delta));
    return sizes.map((size, i) => (i === index ? first : i === index + 1 ? pair - first : size));
  };

  const startDrag = (event: ReactPointerEvent<HTMLDivElement>, divider: Divider) => {
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect || event.button !== 0) return;
    event.preventDefault();
    const handle = event.currentTarget;
    handle.setPointerCapture(event.pointerId);
    const total = divider.axis === 'x' ? rect.width : rect.height;
    const origin = divider.axis === 'x' ? event.clientX : event.clientY;
    let latest = divider.sizes;
    const onMove = (e: PointerEvent) => {
      const pos = divider.axis === 'x' ? e.clientX : e.clientY;
      latest = resized(divider, (pos - origin) / total, total);
      setDraft({ key: divider.key, sizes: latest });
    };
    const onEnd = () => {
      handle.removeEventListener('pointermove', onMove);
      handle.removeEventListener('pointerup', onEnd);
      handle.removeEventListener('lostpointercapture', onEnd);
      setDraft(null);
      setDragAxis(null);
      if (latest !== divider.sizes) onResize(divider.key, latest);
    };
    handle.addEventListener('pointermove', onMove);
    handle.addEventListener('pointerup', onEnd);
    handle.addEventListener('lostpointercapture', onEnd);
    setDragAxis(divider.axis);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>, divider: Divider) => {
    const back = divider.axis === 'x' ? 'ArrowLeft' : 'ArrowUp';
    const forward = divider.axis === 'x' ? 'ArrowRight' : 'ArrowDown';
    if (event.key !== back && event.key !== forward) return;
    const rect = containerRef.current?.getBoundingClientRect();
    if (!rect) return;
    event.preventDefault();
    const total = divider.axis === 'x' ? rect.width : rect.height;
    onResize(divider.key, resized(divider, event.key === back ? -KEY_STEP : KEY_STEP, total));
  };

  return (
    <div
      ref={containerRef}
      className={clsx(
        styles['workspace-split'],
        dragAxis && styles['workspace-split--dragging'],
        dragAxis === 'y' && styles['workspace-split--dragging-y'],
      )}
    >
      {openTabs.map((tileId) => {
        const p = placements.get(tileId);
        if (!p) {
          return (
            <div key={tileId} className={styles['tile-hidden']}>
              {renderView(tileId, false)}
            </div>
          );
        }
        return (
          <div
            key={tileId}
            style={{
              top: pct(p.top),
              height: pct(p.height),
              left: pct(p.left),
              width: pct(p.width),
            }}
            className={clsx(
              styles.tile,
              p.topBorder && styles['tile--top-border'],
              p.leftBorder && styles['tile--left-border'],
            )}
          >
            {renderView(tileId, true)}
          </div>
        );
      })}
      {dividers.map((divider) => {
        const pairIndex = divider.index;
        const value = Math.round(
          (divider.sizes[pairIndex] / (divider.sizes[pairIndex] + divider.sizes[pairIndex + 1])) *
            100,
        );
        return (
          <div
            key={`${divider.key}:${pairIndex}`}
            role="separator"
            tabIndex={0}
            aria-orientation={divider.axis === 'x' ? 'vertical' : 'horizontal'}
            aria-label="Resize panes"
            aria-valuenow={value}
            aria-valuemin={0}
            aria-valuemax={100}
            title="Drag to resize · double-click to even out"
            className={clsx(styles.divider, styles[`divider--${divider.axis}`])}
            style={
              divider.axis === 'x'
                ? {
                    left: pct(divider.at),
                    top: pct(divider.spanStart),
                    height: pct(divider.spanSize),
                  }
                : { top: pct(divider.at), left: 0, width: '100%' }
            }
            onPointerDown={(e) => startDrag(e, divider)}
            onDoubleClick={() =>
              onResize(divider.key, sizesOrEven(undefined, divider.sizes.length))
            }
            onKeyDown={(e) => onKeyDown(e, divider)}
          />
        );
      })}
    </div>
  );
}
