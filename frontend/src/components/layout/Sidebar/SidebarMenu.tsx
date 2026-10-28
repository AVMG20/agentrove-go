import {
  useEffect,
  useLayoutEffect,
  type AriaRole,
  type KeyboardEvent,
  type ReactNode,
  type Ref,
} from 'react';
import type { LucideIcon } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { DROPDOWN_MARGIN } from '@/config/constants';
import styles from './SidebarMenu.module.scss';

interface SidebarMenuProps {
  ref?: Ref<HTMLDivElement>;
  position: { top: number; left: number };
  onClose?: () => void;
  // A panel that isn't a list of menu items (the folder icon picker).
  role?: AriaRole;
  'aria-label'?: string;
  children: ReactNode;
}

export function SidebarMenu({
  ref,
  position,
  onClose,
  role = 'menu',
  'aria-label': ariaLabel,
  children,
}: SidebarMenuProps) {
  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      onClose?.();
    }
  };

  useEffect(() => {
    const el = ref && typeof ref === 'object' && 'current' in ref ? ref.current : null;
    el?.focus();
  }, [ref]);

  // The opener places the menu for a typical menu size; keep the actual one
  // (e.g. the taller icon picker) on screen. Runs after every render, since the
  // content can change size, and before paint, so it never shows misplaced.
  useLayoutEffect(() => {
    const el = ref && typeof ref === 'object' && 'current' in ref ? ref.current : null;
    if (!el) return;
    const maxTop = window.innerHeight - el.offsetHeight - DROPDOWN_MARGIN;
    const maxLeft = window.innerWidth - el.offsetWidth - DROPDOWN_MARGIN;
    el.style.top = `${Math.max(DROPDOWN_MARGIN, Math.min(position.top, maxTop))}px`;
    el.style.left = `${Math.max(DROPDOWN_MARGIN, Math.min(position.left, maxLeft))}px`;
  });

  return (
    <div
      ref={ref}
      role={role}
      aria-label={ariaLabel}
      tabIndex={-1}
      onKeyDown={handleKeyDown}
      className={styles.menu}
      style={{
        top: `${position.top}px`,
        left: `${position.left}px`,
      }}
    >
      {children}
    </div>
  );
}

export function SidebarMenuDivider() {
  return <div role="separator" className={styles.divider} />;
}

interface SidebarMenuItemProps {
  icon: LucideIcon;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  children: ReactNode;
}

export function SidebarMenuItem({
  icon: Icon,
  onClick,
  danger = false,
  disabled,
  children,
}: SidebarMenuItemProps) {
  return (
    <Button
      onClick={onClick}
      role="menuitem"
      variant="unstyled"
      className={danger ? styles['item-danger'] : styles.item}
      disabled={disabled}
    >
      <Icon className={styles.icon} />
      {children}
    </Button>
  );
}
