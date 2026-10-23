import { Loader2 } from 'lucide-react';
import styles from './LoadingScreen.module.scss';

export function LoadingScreen() {
  return (
    <div className={styles['loading-screen']}>
      <div className={styles.content}>
        <div className={styles.status} role="status" aria-live="polite">
          <Loader2 className={styles.spinner} />
          <p className={styles.label}>Loading...</p>
        </div>
      </div>
    </div>
  );
}
