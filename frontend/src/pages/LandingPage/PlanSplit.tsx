import { useMemo } from 'react';
import clsx from 'clsx';
import { useAccountUsage } from '@/hooks/queries/useUsageQueries';
import { useNow } from '@/hooks/useNow';
import { clampPercent, summarizeWindow, windowLabels } from '@/utils/planUsage';
import type { UsageDevice } from '@/types/usage.types';
import styles from './PlanSplit.module.scss';

interface Person {
  name: string;
  color: number;
  used: number;
  blocked: boolean;
}

// A device's owner is its first word without a possessive: "Ramon's Laptop"
// and "Ramon Linux" are both Ramon.
function ownerOf(device: string): string {
  return (device.trim().split(/\s+/)[0] ?? device).replace(/['’]s$/i, '');
}

// Each person keeps the color of the device they use most, so the split
// matches the proxy's admin page.
function groupByPerson(devices: UsageDevice[]): Person[] {
  const people = new Map<string, Person>();
  // Each person's biggest single device so far.
  const top = new Map<string, number>();
  for (const device of devices) {
    const name = ownerOf(device.name);
    const key = name.toLowerCase();
    const used = clampPercent(device.used_percent);
    const person = people.get(key) ?? { name, color: device.color, used: 0, blocked: false };
    person.used += used;
    person.blocked ||= device.blocked;
    if (used > (top.get(key) ?? -1)) {
      top.set(key, used);
      person.color = device.color;
    }
    people.set(key, person);
  }
  return [...people.values()].sort((a, b) => b.used - a.used);
}

// The proxy's color slots are 1-based; five tints, then they repeat.
const colorClass = (slot: number) => styles[`color--${(((slot - 1) % 5) + 5) % 5}`];

const formatPercent = (value: number) =>
  `${value < 10 && value > 0 ? value.toFixed(1) : Math.round(value)}%`;

// Who used how much of a shared plan this week: one stacked bar under the
// greeting, a segment per person. Only for proxies that report devices.
export function PlanSplit({ profileId }: { profileId: string }) {
  const { usage } = useAccountUsage(profileId, Boolean(profileId));
  const people = useMemo(() => groupByPerson(usage?.devices ?? []), [usage?.devices]);
  const now = useNow(30_000);
  if (usage?.status !== 'ok' || people.length === 0) return null;

  const weekly = usage.windows.find((w) => windowLabels(w.window_seconds).short === 'wk');
  const total = people.reduce((sum, person) => sum + person.used, 0);
  const left = Math.max(0, 100 - Math.max(total, weekly ? clampPercent(weekly.used_percent) : 0));
  const resetLabel = weekly ? summarizeWindow(weekly, now).resetLabel : null;
  const leftLabel = `${formatPercent(left)} left`;
  const summary = people.map((p) => `${p.name} ${formatPercent(p.used)}`).join(', ');
  const resetText = resetLabel ? `, resets in ${resetLabel}` : '';

  return (
    <div className={styles.split} aria-label={`This week: ${summary}, ${leftLabel}${resetText}`}>
      <div className={styles.bar} aria-hidden="true">
        {people.map((person) => (
          <span
            key={person.name}
            className={clsx(styles.segment, colorClass(person.color))}
            style={{ width: `${Math.min(100, person.used)}%` }}
          />
        ))}
      </div>
      <div className={styles.legend}>
        {people.map((person) => (
          <span key={person.name} className={styles.person}>
            <span className={clsx(styles.dot, colorClass(person.color))} />
            {person.name}
            <span className={clsx(styles.figure, person.blocked && styles.blocked)}>
              {formatPercent(person.used)}
            </span>
          </span>
        ))}
        <span className={styles.left}>{leftLabel}</span>
      </div>
      {resetLabel && <span className={styles.reset}>Resets in {resetLabel}</span>}
    </div>
  );
}
