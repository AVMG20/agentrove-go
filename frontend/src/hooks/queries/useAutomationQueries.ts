import { useQuery, type QueryClient } from '@tanstack/react-query';
import { automationService } from '@/services/automationService';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { createMutation } from '@/hooks/queries/createMutation';
import { snapshotQueries } from '@/hooks/queries/optimistic';
import type {
  Automation,
  AutomationCreateRequest,
  AutomationUpdateRequest,
} from '@/types/automation.types';

function invalidateAutomations(queryClient: QueryClient) {
  void queryClient.invalidateQueries({ queryKey: queryKeys.automations });
}

export const useAutomationsQuery = () =>
  useQuery<Automation[]>({
    queryKey: queryKeys.automations,
    queryFn: () => automationService.listAutomations(),
    staleTime: 30_000,
  });

export const useCreateAutomationMutation = createMutation<
  Automation,
  Error,
  AutomationCreateRequest
>(
  (data) => automationService.createAutomation(data),
  (queryClient) => invalidateAutomations(queryClient),
);

// Shared by the optimistic edits below: only the last in flight refetches.
const automationMutationKey = ['automation-mutation'] as const;

async function applyAutomationsChange(
  queryClient: QueryClient,
  update: (automations: Automation[]) => Automation[],
) {
  const snapshot = await snapshotQueries(queryClient, [{ queryKey: queryKeys.automations }]);
  queryClient.setQueryData<Automation[]>(queryKeys.automations, (old) => old && update(old));
  return snapshot;
}

// Optimistic, so the enable switch (and a saved edit) shows at once; the
// server's next_run_at arrives with the refetch.
export const useUpdateAutomationMutation = createMutation<
  Automation,
  Error,
  { automationId: string; data: AutomationUpdateRequest }
>(
  ({ automationId, data }) => automationService.updateAutomation(automationId, data),
  () => undefined,
  {
    mutationKey: automationMutationKey,
    scope: 'automation-update',
    apply: (queryClient, { automationId, data }) =>
      applyAutomationsChange(queryClient, (automations) =>
        automations.map((a) => (a.id === automationId ? { ...a, ...data } : a)),
      ),
    settle: invalidateAutomations,
  },
);

export const useDeleteAutomationMutation = createMutation<void, Error, string>(
  (automationId) => automationService.deleteAutomation(automationId),
  () => undefined,
  {
    mutationKey: automationMutationKey,
    apply: (queryClient, automationId) =>
      applyAutomationsChange(queryClient, (automations) =>
        automations.filter((a) => a.id !== automationId),
      ),
    settle: invalidateAutomations,
  },
);

export const useRunAutomationMutation = createMutation<{ chat_id: string }, Error, string>(
  (automationId) => automationService.runAutomation(automationId),
  (queryClient) => {
    invalidateAutomations(queryClient);
    void queryClient.invalidateQueries({ queryKey: [queryKeys.chats] });
  },
);
