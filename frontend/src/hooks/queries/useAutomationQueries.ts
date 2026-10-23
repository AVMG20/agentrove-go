import { useQuery, type QueryClient } from '@tanstack/react-query';
import { automationService } from '@/services/automationService';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { createMutation } from '@/hooks/queries/createMutation';
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

export const useUpdateAutomationMutation = createMutation<
  Automation,
  Error,
  { automationId: string; data: AutomationUpdateRequest }
>(
  ({ automationId, data }) => automationService.updateAutomation(automationId, data),
  (queryClient) => invalidateAutomations(queryClient),
);

export const useDeleteAutomationMutation = createMutation<void, Error, string>(
  (automationId) => automationService.deleteAutomation(automationId),
  (queryClient) => invalidateAutomations(queryClient),
);

export const useRunAutomationMutation = createMutation<{ chat_id: string }, Error, string>(
  (automationId) => automationService.runAutomation(automationId),
  (queryClient) => {
    invalidateAutomations(queryClient);
    void queryClient.invalidateQueries({ queryKey: [queryKeys.chats] });
  },
);
