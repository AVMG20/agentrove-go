import { Link as RouterLink } from 'react-router-dom';
import { KeyRound } from 'lucide-react';
import clsx from 'clsx';
import { settingsPath } from '@/components/settings/settingsSections';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import styles from './GitHubTokenHint.module.scss';

// Shown where a missing GitHub token limits what works: cloning private repos
// and opening PRs (in the box, git and gh authenticate with it).
export function GitHubTokenHint({
  className,
  onNavigate,
}: {
  className?: string;
  // Lets a dialog close itself when the user follows the link.
  onNavigate?: () => void;
}) {
  const { data: settings, isSuccess } = useSettingsQuery();
  if (!isSuccess || settings?.github_personal_access_token) return null;
  return (
    <p className={clsx(styles.hint, className)}>
      <KeyRound className={styles.icon} aria-hidden="true" />
      <span>
        Add a GitHub token in{' '}
        <RouterLink to={settingsPath('general')} className={styles.link} onClick={onNavigate}>
          Settings
        </RouterLink>{' '}
        to clone private repos and open PRs
      </span>
    </p>
  );
}
