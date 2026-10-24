import { useRef } from 'react';
import { Image } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { useIsMobile } from '@/hooks/useIsMobile';
import styles from './AttachButton.module.scss';

export interface AttachButtonProps {
  onAttach?: () => void;
  // Given, phones skip the drop-zone dialog (no drag and drop there) and open the
  // system picker (photos, camera, files) straight away, adding what's picked.
  onFiles?: (files: File[]) => void;
  disabled?: boolean;
}

export function AttachButton({ onAttach, onFiles, disabled = false }: AttachButtonProps) {
  const isMobile = useIsMobile();
  const inputRef = useRef<HTMLInputElement>(null);
  const pickDirectly = isMobile && !!onFiles;

  return (
    <>
      <Button
        type="button"
        onClick={pickDirectly ? () => inputRef.current?.click() : onAttach}
        variant="unstyled"
        disabled={disabled}
        className={styles['attach-button']}
        aria-label="Attach file"
      >
        <Image className={styles.icon} />
      </Button>
      {pickDirectly && (
        <input
          ref={inputRef}
          type="file"
          accept="image/*,application/pdf,.xlsx"
          multiple
          tabIndex={-1}
          aria-hidden
          className={styles['file-input']}
          onChange={(e) => {
            const files = Array.from(e.currentTarget.files ?? []);
            e.currentTarget.value = '';
            if (files.length > 0) onFiles(files);
          }}
        />
      )}
    </>
  );
}
