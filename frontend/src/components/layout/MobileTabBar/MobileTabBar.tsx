import { MessageSquare, type LucideIcon } from 'lucide-react';
import { useUIStore } from '@/store/uiStore';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import { paintedLayout, tileIdToViewType, VIEW_ICONS } from '@/utils/tileHelpers';
import type { ViewType } from '@/types/ui.types';
import styles from './MobileTabBar.module.scss';

const TABS: { view: ViewType; label: string; icon: LucideIcon }[] = [
  { view: 'agent', label: 'Chat', icon: MessageSquare },
  { view: 'diff', label: 'Changes', icon: VIEW_ICONS.diff },
  { view: 'editor', label: 'Files', icon: VIEW_ICONS.editor },
  { view: 'terminal', label: 'Terminal', icon: VIEW_ICONS.terminal },
  { view: 'browser', label: 'Browser', icon: VIEW_ICONS.browser },
];

// Phones show one workspace view at a time; this bar switches between them
// (the desktop title bar's view buttons) within thumb reach, and always offers
// the way back to the chat. Hidden while the on-screen keyboard is up.
export function MobileTabBar() {
  const visibleLayout = useUIStore((s) => s.visibleLayout);
  const focusedTile = useUIStore((s) => s.focusedTile);
  // Hidden when the server runs no box browsers; shown until settings load.
  const browserAvailable = useSettingsQuery().data?.browser_available !== false;
  const shown = paintedLayout(visibleLayout, focusedTile, true)[0]?.[0];
  const active = shown ? tileIdToViewType(shown) : 'agent';

  return (
    <nav className={styles.bar} aria-label="Views">
      {TABS.map(({ view, label, icon: Icon }) => {
        if (view === 'browser' && !browserAvailable) return null;
        const isActive = active === view;
        return (
          <button
            key={view}
            type="button"
            className={styles.tab}
            aria-current={isActive ? 'page' : undefined}
            onClick={() => useUIStore.getState().toggleView(view, false)}
          >
            <Icon className={styles.icon} aria-hidden />
            <span className={styles.label}>{label}</span>
          </button>
        );
      })}
    </nav>
  );
}
