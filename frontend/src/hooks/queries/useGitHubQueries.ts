import { useMemo } from 'react';
import { useMutation, useQuery, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import { githubService } from '@/services/githubService';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { useGitRemoteUrlQuery } from '@/hooks/queries/useSandboxQueries';
import type {
  CreatePRRequest,
  CreatePRResponse,
  GenerateCommitMessageRequest,
  GenerateCommitMessageResponse,
  GeneratePRDescriptionRequest,
  GeneratePRDescriptionResponse,
  PullRequestFilters,
} from '@/types/github.types';

export function useGitHubReposQuery(query: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.github.repos(query),
    queryFn: () => githubService.searchRepositories(query, 1, 20),
    enabled,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}

export function useGitHubPullsQuery(
  owner: string,
  repo: string,
  enabled: boolean,
  filters?: PullRequestFilters,
) {
  return useQuery({
    queryKey: queryKeys.github.pulls(owner, repo, filters),
    queryFn: () => githubService.listPullRequests(owner, repo, filters),
    enabled: enabled && !!owner && !!repo,
    staleTime: 30_000,
  });
}

export function useGitHubCollaboratorsQuery(owner: string, repo: string, enabled: boolean) {
  return useQuery({
    queryKey: queryKeys.github.collaborators(owner, repo),
    queryFn: () => githubService.getCollaborators(owner, repo),
    enabled: enabled && !!owner && !!repo,
    staleTime: 300_000,
  });
}

export function useBranchPullRequests(
  sandboxId: string,
  worktreeCwd: string | undefined,
  branch: string,
) {
  const { data: remoteUrl } = useGitRemoteUrlQuery(sandboxId, !!sandboxId, worktreeCwd);
  const owner = remoteUrl?.owner ?? '';
  const repo = remoteUrl?.repo ?? '';
  const { data: pulls } = useGitHubPullsQuery(owner, repo, !!owner && !!repo && !!branch, {
    head: `${owner}:${branch}`,
    state: 'all',
  });
  const { openPR, mergedPR } = useMemo(() => {
    const branchPulls =
      branch && owner && repo
        ? pulls?.items.filter(
            (pr) => pr.head.ref === branch && pr.head.repo.full_name === `${owner}/${repo}`,
          )
        : undefined;
    return {
      openPR: branchPulls?.find((pr) => pr.state === 'open'),
      mergedPR: branchPulls?.find((pr) => pr.merged),
    };
  }, [pulls?.items, branch, owner, repo]);
  return { owner, repo, openPR, mergedPR };
}

export function useCreatePullRequestMutation() {
  const queryClient = useQueryClient();
  return useMutation<CreatePRResponse, Error, CreatePRRequest>({
    mutationFn: (request) => githubService.createPullRequest(request),
    onSuccess: (_data, { owner, repo }) =>
      queryClient.invalidateQueries({ queryKey: queryKeys.github.pullsAll(owner, repo) }),
  });
}

export function useGeneratePRDescriptionMutation() {
  return useMutation<GeneratePRDescriptionResponse, Error, GeneratePRDescriptionRequest>({
    mutationFn: (request) => githubService.generatePRDescription(request),
  });
}

export function useGenerateCommitMessageMutation() {
  return useMutation<GenerateCommitMessageResponse, Error, GenerateCommitMessageRequest>({
    mutationFn: (request) => githubService.generateCommitMessage(request),
  });
}
