import { ArrowLeft } from 'lucide-react';
import { NavLink } from 'react-router-dom';
import { Button } from '@/components/ui/primitives/Button/Button';
import { SETTINGS_SECTIONS, settingsPath } from '@/components/settings/settingsSections';
import styles from './SettingsNav.module.scss';

interface SettingsNavProps {
  onBack: () => void;
  onNavigate: () => void;
}

// The sidebar body while a settings route is open: the sections, plus a way
// back to the thread list the user came from.
export function SettingsNav({ onBack, onNavigate }: SettingsNavProps) {
  return (
    <nav className={styles.nav} aria-label="Settings">
      <div className={styles.top}>
        <Button variant="unstyled" onClick={onBack} className={styles.back}>
          <ArrowLeft className={styles['back-icon']} />
          Back to threads
        </Button>
      </div>

      <div className={styles.heading}>Settings</div>

      <ul className={styles.list}>
        {SETTINGS_SECTIONS.map(({ id, label, icon: Icon }) => (
          <li key={id}>
            <NavLink to={settingsPath(id)} className={styles.item} onClick={onNavigate}>
              <Icon className={styles['item-icon']} />
              {label}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
