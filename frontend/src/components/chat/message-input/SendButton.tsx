import { ArrowUp, LoaderCircle, Square } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import styles from './SendButton.module.scss';

export type SendButtonStatus = 'idle' | 'ready' | 'loading' | 'streaming';

export interface SendButtonProps {
  status: SendButtonStatus;
  disabled: boolean;
  onClick: (e: React.MouseEvent) => void;
  type?: 'button' | 'submit';
  className?: string;
  showLoadingSpinner?: boolean;
  // Sending while a turn runs queues the message instead.
  queues?: boolean;
}

export function SendButton({
  status,
  disabled,
  onClick,
  type = 'button',
  className,
  showLoadingSpinner = false,
  queues = false,
}: SendButtonProps) {
  const isActive = status === 'loading' || status === 'streaming';
  const hasMessage = status === 'ready';

  const showSpinnerIcon = showLoadingSpinner && status === 'loading';
  const showStopIcon = !showSpinnerIcon && isActive;

  const isPrimary = showSpinnerIcon || showStopIcon || hasMessage;
  let ariaLabel: string;
  let icon: React.ReactNode;

  if (showSpinnerIcon) {
    ariaLabel = 'Starting chat';
    icon = <LoaderCircle className={styles['icon-spinner']} />;
  } else if (showStopIcon) {
    ariaLabel = 'Stop generating';
    icon = <Square className={styles['icon-stop']} />;
  } else {
    ariaLabel = queues ? 'Queue message' : 'Send message';
    icon = (
      <ArrowUp className={clsx(styles['icon-send'], hasMessage && styles['icon-send--active'])} />
    );
  }

  return (
    <Button
      type={type}
      onClick={onClick}
      disabled={disabled}
      variant="unstyled"
      className={clsx(
        styles['send-button'],
        isPrimary ? styles['send-button--primary'] : styles['send-button--idle'],
        hasMessage && !disabled && styles['send-button--ready'],
        className,
      )}
      aria-label={ariaLabel}
    >
      {icon}
    </Button>
  );
}

interface StopButtonProps {
  onClick: (e: React.MouseEvent) => void;
}

// Separate from send so a turn can be stopped while a draft is in the composer.
export function StopButton({ onClick }: StopButtonProps) {
  return (
    <Button
      type="button"
      onClick={onClick}
      variant="unstyled"
      className={clsx(styles['send-button'], styles['send-button--stop'])}
      aria-label="Stop generating"
      title="Stop (Esc)"
    >
      <Square className={styles['icon-stop-outline']} />
    </Button>
  );
}
