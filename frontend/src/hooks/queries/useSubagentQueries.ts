import { useQuery, type QueryClient } from '@tanstack/react-query';
import { chatService } from '@/services/chatService';
import type { Subagent } from '@/types/chat.types';
import { queryKeys } from './queryKeys';

// Newest first, like the server.
function sortSubagents(subagents: Subagent[]): Subagent[] {
  return [...subagents].sort((a, b) => b.started_at - a.started_at || b.id.localeCompare(a.id));
}

// The chat's subagents (session panel). Kept live by `subagent` chat events.
export const useChatSubagentsQuery = (chatId: string | undefined) =>
  useQuery<Subagent[]>({
    queryKey: queryKeys.subagents(chatId),
    queryFn: () => chatService.getSubagents(chatId!),
    enabled: !!chatId,
    staleTime: Infinity,
  });

// One subagent with its prompt and activity, while its pane is on screen.
export const useSubagentQuery = (
  chatId: string | undefined,
  subagentId: string | undefined,
  enabled: boolean,
) =>
  useQuery<Subagent>({
    queryKey: queryKeys.subagent(chatId, subagentId),
    queryFn: () => chatService.getSubagent(chatId!, subagentId!),
    enabled: enabled && !!chatId && !!subagentId,
    // A deleted chat's subagent won't come back on retry.
    retry: false,
  });

// Applies a `subagent` chat event: the summary replaces the list entry and an
// open pane refetches the detail (the server sends at most one event per
// subagent per flush, so this stays cheap).
export function applySubagentEvent(queryClient: QueryClient, chatId: string, subagent: Subagent) {
  queryClient.setQueryData<Subagent[]>(queryKeys.subagents(chatId), (current) => {
    if (!current) return current;
    const rest = current.filter((s) => s.id !== subagent.id);
    return sortSubagents([subagent, ...rest]);
  });
  void queryClient.invalidateQueries({
    queryKey: queryKeys.subagent(chatId, subagent.id),
    refetchType: 'active',
  });
}

export function useSubagentSummary(
  chatId: string | undefined,
  subagentId: string,
): Subagent | undefined {
  const { data } = useChatSubagentsQuery(chatId);
  return data?.find((s) => s.id === subagentId);
}
