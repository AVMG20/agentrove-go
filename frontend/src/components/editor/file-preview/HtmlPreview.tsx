import { memo, useState } from 'react';
import type { FileStructure } from '@/types/file-system.types';
import { useAsyncEffect } from '@/hooks/useAsyncEffect';
import { PreviewContainer } from './PreviewContainer';
import { PreviewEmptyState } from './PreviewEmptyState';
import { getDisplayFileName } from './previewUtils';
import styles from './HtmlPreview.module.scss';

interface HtmlPreviewProps {
  file: FileStructure;
  isFullscreen?: boolean;
  onToggleFullscreen?: () => void;
}

export const HtmlPreview = memo(function HtmlPreview({
  file,
  isFullscreen = false,
  onToggleFullscreen,
}: HtmlPreviewProps) {
  const [sanitizedContent, setSanitizedContent] = useState('');

  useAsyncEffect(
    async (cancelled) => {
      setSanitizedContent('');

      if (!file.content) {
        return;
      }

      const DOMPurify = (await import('dompurify')).default;
      if (cancelled()) return;
      setSanitizedContent(
        DOMPurify.sanitize(file.content, {
          WHOLE_DOCUMENT: true,
          ADD_TAGS: ['style', 'link'],
          ADD_ATTR: ['target', 'rel'],
        }),
      );
    },
    [file.content],
  );

  if (!file.content) {
    return (
      <PreviewEmptyState
        fileName={getDisplayFileName(file)}
        message="No content to preview"
        isFullscreen={isFullscreen}
        onToggleFullscreen={onToggleFullscreen}
      />
    );
  }

  return (
    <PreviewContainer
      fileName={getDisplayFileName(file)}
      isFullscreen={isFullscreen}
      onToggleFullscreen={onToggleFullscreen}
    >
      {/* Fully sandboxed: agent-written HTML gets an opaque origin (no access to the
          app's localStorage/cookies) and no scripts — DOMPurify strips them anyway, so
          the preview is a static render of the markup + styles. */}
      <iframe
        srcDoc={sanitizedContent}
        className={styles.iframe}
        title={`HTML Preview: ${file.path}`}
        sandbox=""
        referrerPolicy="no-referrer"
      />
    </PreviewContainer>
  );
});
