import type { ReactNode } from 'react';
import clsx from 'clsx';
import { ChevronDown } from 'lucide-react';
import { useUIStore } from '@/store/uiStore';
import styles from './SidebarSection.module.scss';

interface SidebarSectionProps {
  // Persisted collapse key (uiStore.sidebarCollapsed)
  sectionKey: string;
  title: string;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}

export function SidebarSection({
  sectionKey,
  title,
  actions,
  children,
  className,
}: SidebarSectionProps) {
  const collapsed = useUIStore((state) => state.sidebarCollapsed.includes(sectionKey));

  return (
    <section className={clsx(styles.section, className)}>
      <div className={styles.header}>
        <button
          type="button"
          className={styles.toggle}
          aria-expanded={!collapsed}
          onClick={() => useUIStore.getState().toggleSidebarCollapsed(sectionKey)}
        >
          <span className={styles.title}>{title}</span>
          <ChevronDown
            className={clsx(styles.chevron, collapsed && styles['chevron--collapsed'])}
          />
        </button>
        {actions && <div className={styles.actions}>{actions}</div>}
      </div>
      {!collapsed && children}
    </section>
  );
}
