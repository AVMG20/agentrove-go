import { useMemo, useState } from 'react';
import { Loader2, Monitor, Smartphone } from 'lucide-react';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import {
  useRevokeOtherSessionsMutation,
  useRevokeSessionMutation,
  useSessionsQuery,
} from '@/hooks/queries/useAuthQueries';
import type { AuthSession } from '@/types/user.types';
import { formatFullTimestamp, formatRelativeTime } from '@/utils/date';
import { describeUserAgent, isMobileUserAgent } from '@/utils/userAgent';
import styles from './SessionsSettingsTab.module.scss';

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export function SessionsSettingsTab() {
  const { data, error } = useSessionsQuery();
  const revokeSession = useRevokeSessionMutation();
  const revokeOthers = useRevokeOtherSessionsMutation();
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [confirmingRevokeAll, setConfirmingRevokeAll] = useState(false);

  // This device first, then the server's most-recently-used order.
  const sessions = useMemo(
    () => (data ? [...data].sort((a, b) => Number(b.current) - Number(a.current)) : null),
    [data],
  );
  const otherCount = sessions?.filter((session) => !session.current).length ?? 0;

  const handleRevoke = async (session: AuthSession) => {
    setRevokingId(session.id);
    try {
      await revokeSession.mutateAsync(session.id);
      toast.success('Session revoked');
    } catch (err) {
      toast.error(errorMessage(err, 'Failed to revoke session'));
    } finally {
      setRevokingId(null);
    }
  };

  const handleRevokeOthers = async () => {
    try {
      await revokeOthers.mutateAsync();
      toast.success('Signed out of all other sessions');
    } catch (err) {
      toast.error(errorMessage(err, 'Failed to revoke sessions'));
    } finally {
      setConfirmingRevokeAll(false);
    }
  };

  return (
    <div className={styles.tab}>
      <div className={styles['header-row']}>
        <div>
          <h2 className={styles.title}>Sessions</h2>
          <p className={styles.description}>
            Devices signed in to your account. Revoking a session signs that device out within a few
            minutes.
          </p>
        </div>
        {otherCount > 0 && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className={styles['revoke-all-btn']}
            onClick={() => setConfirmingRevokeAll(true)}
            isLoading={revokeOthers.isPending}
          >
            Revoke all other sessions
          </Button>
        )}
      </div>

      {sessions === null ? (
        error ? (
          <p className={styles.status}>{errorMessage(error, 'Failed to load sessions')}</p>
        ) : (
          <div className={styles.loading}>
            <Loader2 className={styles['loading-icon']} />
          </div>
        )
      ) : (
        <ul className={styles.items}>
          {sessions.map((session) => {
            const DeviceIcon = isMobileUserAgent(session.user_agent) ? Smartphone : Monitor;
            return (
              <li key={session.id} className={styles.card}>
                <DeviceIcon className={styles['device-icon']} aria-hidden="true" />
                <div className={styles['card-content']}>
                  <div className={styles['item-header']}>
                    <h3 className={styles['item-title']} title={session.user_agent || undefined}>
                      {describeUserAgent(session.user_agent)}
                    </h3>
                    {session.current && <span className={styles.badge}>This device</span>}
                  </div>
                  <div className={styles['item-meta']}>
                    {session.ip_address && <span>{session.ip_address}</span>}
                    <span title={formatFullTimestamp(session.created_at)}>
                      Signed in {formatRelativeTime(session.created_at)}
                    </span>
                    {!session.current && (
                      <span
                        className={styles.muted}
                        title={formatFullTimestamp(session.last_used_at)}
                      >
                        Last active {formatRelativeTime(session.last_used_at)}
                      </span>
                    )}
                  </div>
                </div>
                {!session.current && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void handleRevoke(session)}
                    isLoading={revokingId === session.id}
                    aria-label={`Revoke ${describeUserAgent(session.user_agent)}`}
                  >
                    Revoke
                  </Button>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <ConfirmDialog
        isOpen={confirmingRevokeAll}
        onClose={() => setConfirmingRevokeAll(false)}
        onConfirm={() => void handleRevokeOthers()}
        title="Revoke other sessions"
        message={`Sign out ${otherCount === 1 ? 'the other device' : `all ${otherCount} other devices`}? This device stays signed in.`}
        confirmLabel="Revoke all"
        cancelLabel="Cancel"
      />
    </div>
  );
}
