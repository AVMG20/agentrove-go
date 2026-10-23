import { formatNumberCompact } from '@/utils/format';

export type ContextUsageTone = 'normal' | 'warning' | 'danger';

export interface ContextUsageInfo {
  tokensUsed: number;
  contextWindow: number;
}

export function getContextUsageDisplay({ tokensUsed, contextWindow }: ContextUsageInfo) {
  const percentage = contextWindow > 0 ? Math.min((tokensUsed / contextWindow) * 100, 100) : 0;
  const tone: ContextUsageTone =
    percentage >= 95 ? 'danger' : percentage >= 75 ? 'warning' : 'normal';
  const formatted =
    percentage === 0
      ? '0'
      : percentage >= 10
        ? percentage.toFixed(0)
        : percentage >= 1
          ? percentage.toFixed(1)
          : percentage.toFixed(2);
  return {
    percentage,
    tone,
    label: `${formatted}%`,
    tooltip: `${formatNumberCompact(tokensUsed)} / ${formatNumberCompact(contextWindow)} tokens`,
  };
}
