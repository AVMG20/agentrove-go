import { useEffect, useId, useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, ExternalLink, Loader2, XCircle } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import {
  useCancelProfileLoginMutation,
  useProfileLoginQuery,
  useSendProfileLoginInputMutation,
} from '@/hooks/queries/useProfileQueries';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { PROVIDER_CLI } from '@/utils/profiles';
import type { Profile } from '@/types/profile.types';
import styles from './ProvidersSettingsTab.module.scss';

interface ProfileSignInProps {
  profile: Profile;
  // POST /login is still running (it may first boot a stopped box): there is
  // nothing to poll yet.
  starting: boolean;
  onClose: () => void;
  onRetry: () => void;
}

function signInIntro(profile: Profile): string {
  if (profile.provider === 'codex') {
    return 'Open the link, sign in to ChatGPT and enter the code shown below.';
  }
  if (profile.provider === 'antigravity') {
    return 'Open the link and sign in with Google. If the CLI asks for a code or a choice, answer below.';
  }
  return 'Open the link and sign in to Claude. Paste the code it shows below.';
}

// A sign-in running in the box: the CLI's link, device code and output, plus
// a field to answer its prompts.
export function ProfileSignIn({ profile, starting, onClose, onRetry }: ProfileSignInProps) {
  const id = useId();
  const queryClient = useQueryClient();
  const { data: login, error: loginError } = useProfileLoginQuery(profile.id, !starting);
  const sendInput = useSendProfileLoginInputMutation();
  const cancel = useCancelProfileLoginMutation();
  const [answer, setAnswer] = useState('');
  const [inputError, setInputError] = useState<string | null>(null);

  const status = starting ? undefined : login?.status;
  // A finished sign-in changes the account's status.
  useEffect(() => {
    if (status && status !== 'running') {
      void queryClient.invalidateQueries({ queryKey: queryKeys.profiles });
      void queryClient.invalidateQueries({ queryKey: queryKeys.claudeUsage });
    }
  }, [status, queryClient]);

  const handleSend = (event: FormEvent) => {
    event.preventDefault();
    const text = answer.trim();
    if (!text) return;
    setInputError(null);
    sendInput.mutate(
      { profileId: profile.id, text },
      {
        onSuccess: () => setAnswer(''),
        onError: (error) => setInputError(error.message),
      },
    );
  };

  const handleCancel = () => {
    cancel.mutate(profile.id, { onSettled: onClose });
  };

  if (starting) {
    return (
      <div className={styles.signin} role="status">
        <p className={styles['signin-text']}>
          <Loader2 aria-hidden className={clsx(styles['signin-icon'], styles.spin)} />
          Starting your box and {PROVIDER_CLI[profile.provider]}…
        </p>
      </div>
    );
  }

  if (!login) {
    return (
      <div className={styles.signin} role="status">
        {loginError ? (
          <div className={styles['signin-row']}>
            <p className={styles['signin-text']}>This sign-in is no longer running.</p>
            <div className={styles['signin-actions']}>
              <Button type="button" variant="ghost" size="sm" onClick={onClose}>
                Close
              </Button>
              <Button type="button" variant="outline" size="sm" onClick={onRetry}>
                Start again
              </Button>
            </div>
          </div>
        ) : (
          <p className={styles['signin-text']}>
            <Loader2 aria-hidden className={clsx(styles['signin-icon'], styles.spin)} />
            Starting {PROVIDER_CLI[profile.provider]} in your box…
          </p>
        )}
      </div>
    );
  }

  if (status === 'succeeded') {
    return (
      <div className={clsx(styles.signin, styles['signin--done'])} role="status">
        <div className={styles['signin-row']}>
          <p className={styles['signin-text']}>
            <CheckCircle2 aria-hidden className={clsx(styles['signin-icon'], styles.ok)} />
            Signed in.
          </p>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Done
          </Button>
        </div>
      </div>
    );
  }

  if (status === 'failed' || status === 'cancelled') {
    return (
      <div className={clsx(styles.signin, styles['signin--failed'])} role="alert">
        <p className={styles['signin-text']}>
          <XCircle aria-hidden className={clsx(styles['signin-icon'], styles.bad)} />
          {status === 'cancelled' ? 'Sign-in cancelled.' : login.error || 'Sign-in failed.'}
        </p>
        {login.output && <pre className={styles['signin-output']}>{login.output}</pre>}
        <div className={styles['signin-actions']}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Close
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.signin} aria-live="polite">
      <p className={styles['signin-text']}>
        <Loader2 aria-hidden className={clsx(styles['signin-icon'], styles.spin)} />
        {login.url ? signInIntro(profile) : `Waiting for ${PROVIDER_CLI[profile.provider]}…`}
      </p>

      {(login.url || login.code) && (
        <div className={styles['signin-links']}>
          {login.url && (
            <a
              href={login.url}
              target="_blank"
              rel="noopener noreferrer"
              className={styles['signin-link']}
            >
              Open sign-in page
              <ExternalLink aria-hidden className={styles['signin-link-icon']} />
            </a>
          )}
          {login.code && (
            <span className={styles['signin-code']}>
              Code <code>{login.code}</code>
            </span>
          )}
        </div>
      )}

      <form className={styles['signin-form']} onSubmit={handleSend}>
        <label htmlFor={`${id}-answer`} className={styles['sr-only']}>
          Code or answer for the sign-in
        </label>
        <Input
          id={`${id}-answer`}
          value={answer}
          onChange={(e) => setAnswer(e.target.value)}
          placeholder="Paste a code or type an answer"
          autoComplete="off"
          spellCheck={false}
        />
        <Button
          type="submit"
          variant="outline"
          size="sm"
          disabled={!answer.trim() || sendInput.isPending}
          isLoading={sendInput.isPending}
        >
          Send
        </Button>
      </form>
      {inputError && (
        <p className={styles['signin-error']} role="alert">
          {inputError}
        </p>
      )}

      {login.output && (
        <details className={styles['signin-details']}>
          <summary>Terminal output</summary>
          <pre className={styles['signin-output']}>{login.output}</pre>
        </details>
      )}

      <div className={styles['signin-actions']}>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={handleCancel}
          disabled={cancel.isPending}
        >
          Cancel sign-in
        </Button>
      </div>
    </div>
  );
}
