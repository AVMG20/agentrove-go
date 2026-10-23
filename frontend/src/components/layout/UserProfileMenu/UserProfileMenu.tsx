import { Settings, LogOut } from 'lucide-react';
import { UserAvatarCircle } from '@/components/chat/message-bubble/MessageAvatars';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useUIStore } from '@/store/uiStore';
import { useDropdown } from '@/hooks/useDropdown';
import { getThemeMeta } from '@/utils/theme';
import styles from './UserProfileMenu.module.scss';

interface UserProfileMenuProps {
  displayName: string | undefined;
  onOpenSettings: () => void;
  onSignOut: () => void;
}

export function UserProfileMenu({ displayName, onOpenSettings, onSignOut }: UserProfileMenuProps) {
  const theme = useUIStore((s) => s.theme);
  const themeMeta = getThemeMeta(theme);
  const ThemeIcon = themeMeta.icon;

  const { isOpen, dropdownRef, setIsOpen } = useDropdown();

  return (
    <div ref={dropdownRef} className={styles['user-profile-menu']}>
      <Button
        variant="unstyled"
        onClick={() => setIsOpen((v) => !v)}
        className={styles.trigger}
        aria-label="Open user menu"
      >
        <span className={styles['avatar-wrap']}>
          <UserAvatarCircle displayName={displayName ?? ''} size="large" />
        </span>
        {displayName && <span className={styles.name}>{displayName}</span>}
      </Button>

      {isOpen && (
        <div className={styles.panel}>
          <div className={styles['menu-list']}>
            <Button
              variant="unstyled"
              onClick={() => useUIStore.getState().toggleTheme()}
              className={styles['menu-item']}
            >
              <ThemeIcon className={styles['item-icon']} />
              <span className={styles['item-label']}>Theme</span>
              <span className={styles['item-value']}>{themeMeta.label}</span>
            </Button>
            <Button
              variant="unstyled"
              onClick={() => {
                setIsOpen(false);
                onOpenSettings();
              }}
              className={styles['menu-item']}
            >
              <Settings className={styles['item-icon']} />
              <span className={styles['item-text']}>Settings</span>
            </Button>
            <Button
              variant="unstyled"
              onClick={() => {
                setIsOpen(false);
                onSignOut();
              }}
              className={styles['menu-item']}
            >
              <LogOut className={styles['item-icon']} />
              <span className={styles['item-text']}>Sign out</span>
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
