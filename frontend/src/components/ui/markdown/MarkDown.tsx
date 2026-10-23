import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { useMemo, memo } from 'react';
import type { Options } from 'react-markdown';
import clsx from 'clsx';
import { MARKDOWN_COMPONENTS } from './markdownComponents';
import { MATH_PATTERN, rehypeMentionPills, splitMarkdownBlocks } from './markdownParsing';
import { useMathPlugins } from './mathPlugins';
import styles from './MarkDown.module.scss';

function markdownUrlTransform(url: string, key: string): string {
  const colon = url.indexOf(':');
  if (key === 'href' && colon > 0 && url.slice(0, colon).toLowerCase() === 'file') return url;
  return defaultUrlTransform(url);
}

interface MarkdownBlockProps {
  content: string;
  remarkPlugins: Options['remarkPlugins'];
  rehypePlugins: Options['rehypePlugins'];
}

const MarkdownBlock = memo(function MarkdownBlock({
  content,
  remarkPlugins,
  rehypePlugins,
}: MarkdownBlockProps) {
  // Per-block memo: streaming only re-parses the growing tail block each flush.
  return (
    <ReactMarkdown
      remarkPlugins={remarkPlugins}
      rehypePlugins={rehypePlugins}
      components={MARKDOWN_COMPONENTS}
      urlTransform={markdownUrlTransform}
    >
      {content}
    </ReactMarkdown>
  );
});

interface MarkDownProps {
  content: string;
  className?: string;
  // While streaming, split blocks for cheap incremental re-parse (breaks cross-block refs).
  streaming?: boolean;
  // @mention /command pills — user messages only.
  highlightMentions?: boolean;
}

export const MarkDown = memo(function MarkDown({
  content,
  className,
  streaming = false,
  highlightMentions = false,
}: MarkDownProps) {
  const blocks = useMemo(
    () => (streaming ? splitMarkdownBlocks(content) : [content]),
    [content, streaming],
  );

  // Only include the math plugins when the content actually contains math, so
  // stray `$` characters in ordinary text aren't parsed as TeX.
  // KaTeX is lazy-loaded on first math; until then the TeX shows as plain text.
  const needsMath = useMemo(() => MATH_PATTERN.test(content), [content]);
  const math = useMathPlugins(needsMath);

  const remarkPlugins = useMemo<NonNullable<Options['remarkPlugins']>>(
    () => (math ? [remarkGfm, [math.remarkMath, { singleDollarTextMath: false }]] : [remarkGfm]),
    [math],
  );
  const rehypePlugins = useMemo(
    () => [...(highlightMentions ? [rehypeMentionPills] : []), ...(math ? [math.rehypeKatex] : [])],
    [math, highlightMentions],
  );

  return (
    <div className={clsx(styles.markdown, className)}>
      {blocks.map((block, i) => (
        <MarkdownBlock
          key={i}
          content={block}
          remarkPlugins={remarkPlugins}
          rehypePlugins={rehypePlugins}
        />
      ))}
    </div>
  );
});
