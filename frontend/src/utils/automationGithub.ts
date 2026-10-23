import type { GitHubTriggerEvent } from '@/types/automation.types';

// GitHub-triggered automations reuse cron_expression as their poll interval.

export const GITHUB_EVENT_OPTIONS: { value: GitHubTriggerEvent; label: string }[] = [
  { value: 'pull_request_opened', label: 'Pull request opened' },
  { value: 'issue_opened', label: 'Issue opened' },
  { value: 'issue_comment_created', label: 'New comment on an issue or PR' },
  { value: 'release_published', label: 'Release published' },
];

const SHORT_EVENT_LABELS: Record<GitHubTriggerEvent, string> = {
  pull_request_opened: 'PRs',
  issue_opened: 'Issues',
  issue_comment_created: 'Comments',
  release_published: 'Releases',
};

export const GITHUB_POLL_OPTIONS = [1, 5, 15, 30, 60] as const;
export const DEFAULT_GITHUB_POLL_MINUTES = 5;

export function buildGithubPollCron(minutes: number): string {
  return minutes >= 60 ? '0 * * * *' : `*/${minutes} * * * *`;
}

export function parseGithubPollMinutes(cron: string): number {
  if (cron === '0 * * * *') return 60;
  const match = /^\*\/(\d{1,2}) \* \* \* \*$/.exec(cron);
  return match ? Number(match[1]) : DEFAULT_GITHUB_POLL_MINUTES;
}

export function describeGithubTrigger(repo: string | null, events: string[] | null): string {
  const kinds = (events ?? [])
    .map((event) => SHORT_EVENT_LABELS[event as GitHubTriggerEvent] ?? event)
    .join(', ');
  return `${repo ?? 'GitHub'} · ${kinds || 'no events'}`;
}
