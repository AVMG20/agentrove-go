import clsx from 'clsx';
import type { BoxInfo } from '@/types/workspace.types';
import { formatBytes } from '@/utils/format';
import styles from './BoxStorage.module.scss';

const PARTS = [
  { key: 'disk_bytes', label: 'System', tone: 'system' },
  { key: 'home_bytes', label: 'Home', tone: 'home' },
  { key: 'projects_bytes', label: 'Projects', tone: 'projects' },
] as const;

// The box's size: total, one bar split by part, and a legend with each part.
export function BoxStorage({ box, className }: { box: BoxInfo; className?: string }) {
  if (box.total_bytes == null) return null;
  const total = box.total_bytes;
  const parts = PARTS.flatMap((part) => {
    const bytes = box[part.key];
    return bytes == null ? [] : [{ ...part, bytes }];
  });

  return (
    <div className={clsx(styles.storage, className)}>
      <div className={styles.header}>
        <span>Storage</span>
        <span className={styles.total}>{formatBytes(total)}</span>
      </div>
      <div className={styles.bar} aria-hidden="true">
        {total > 0 &&
          parts.map((part) => (
            <span
              key={part.key}
              className={clsx(styles.segment, styles[`segment--${part.tone}`])}
              style={{ flexGrow: part.bytes / total }}
            />
          ))}
      </div>
      <ul className={styles.legend}>
        {parts.map((part) => (
          <li key={part.key} className={styles.item}>
            <span className={clsx(styles.swatch, styles[`segment--${part.tone}`])} />
            <span className={styles.label}>{part.label}</span>
            <span className={styles.bytes}>{formatBytes(part.bytes)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
