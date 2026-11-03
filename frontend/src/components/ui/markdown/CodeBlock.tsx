import { useState } from 'react';
import type { HTMLAttributes } from 'react';
import clsx from 'clsx';
import { Check, Copy } from 'lucide-react';
import { Button } from '../primitives/Button/Button';
import styles from './CodeBlock.module.scss';

interface CodeBlockProps extends HTMLAttributes<HTMLElement> {
  language: string;
  codeContent: string;
}

export function CodeBlock({ language, codeContent, className, ...props }: CodeBlockProps) {
  // Copied state lives here, not in MarkDown, so a copy click doesn't
  // change the components mapping identity and re-parse every memoized block.
  const [isCopied, setIsCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(codeContent);
    setIsCopied(true);
    setTimeout(() => setIsCopied(false), 2000);
  };

  const CopyIcon = isCopied ? Check : Copy;

  return (
    <div className={styles['code-block']}>
      <div className={styles['code-header']}>
        <span className={styles['code-lang']}>{language}</span>
        <Button
          onClick={handleCopy}
          variant="unstyled"
          className={clsx(styles['code-copy'], isCopied && styles['code-copy--copied'])}
          aria-label="Copy code"
        >
          <CopyIcon aria-hidden className={styles['code-copy-icon']} />
          <span className={styles['code-copy-label']}>{isCopied ? 'Copied' : 'Copy'}</span>
        </Button>
      </div>
      <pre className={styles['code-pre']}>
        <code className={clsx(className, styles['code-content'])} {...props}>
          {codeContent}
        </code>
      </pre>
    </div>
  );
}
