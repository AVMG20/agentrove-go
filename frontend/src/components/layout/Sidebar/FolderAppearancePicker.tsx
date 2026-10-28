import { useId } from 'react';
import clsx from 'clsx';
import { ChevronLeft } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import {
  FOLDER_COLORS,
  FOLDER_ICONS,
  folderColorClass,
  folderColorKey,
  folderIcon,
} from './folderAppearance';
import styles from './FolderAppearancePicker.module.scss';

export interface FolderAppearance {
  icon: string | null;
  color: string | null;
}

interface FolderAppearancePickerProps {
  value: FolderAppearance;
  // Only the picked field; null is the default.
  onChange: (change: Partial<FolderAppearance>) => void;
  onBack: () => void;
}

// Icon and color presets of a project folder, shown in place of its menu.
// Picks apply right away, so the folder in the list previews them.
export function FolderAppearancePicker({ value, onChange, onBack }: FolderAppearancePickerProps) {
  const id = useId();
  const CurrentIcon = folderIcon(value.icon);
  const colorClass = folderColorClass(value.color);

  return (
    <div className={styles.picker}>
      <div className={styles.header}>
        <Button
          variant="unstyled"
          type="button"
          onClick={onBack}
          className={styles.back}
          aria-label="Back to project menu"
        >
          <ChevronLeft className={styles['back-icon']} />
        </Button>
        <span className={styles.title}>Icon &amp; color</span>
      </div>

      <div className={styles.label} id={`${id}-icon`}>
        Icon
      </div>
      <div className={styles.grid} role="group" aria-labelledby={`${id}-icon`}>
        {FOLDER_ICONS.map(({ key, label, icon: Icon }) => (
          <Button
            key={key}
            variant="unstyled"
            type="button"
            className={styles.option}
            aria-label={label}
            aria-pressed={Icon === CurrentIcon}
            title={label}
            // The plain folder is stored as null, like an untouched project.
            onClick={() => onChange({ icon: key === 'folder' ? null : key })}
          >
            <Icon className={clsx(styles['option-icon'], colorClass)} />
          </Button>
        ))}
      </div>

      <div className={styles.label} id={`${id}-color`}>
        Color
      </div>
      <div className={styles.grid} role="group" aria-labelledby={`${id}-color`}>
        {[{ key: null, label: 'Default' }, ...FOLDER_COLORS].map(({ key, label }) => (
          <Button
            key={key ?? 'default'}
            variant="unstyled"
            type="button"
            className={styles.option}
            aria-label={label}
            aria-pressed={key === folderColorKey(value.color)}
            title={label}
            onClick={() => onChange({ color: key })}
          >
            <span className={clsx(styles.swatch, folderColorClass(key))} />
          </Button>
        ))}
      </div>
    </div>
  );
}
