import { apiClient } from '@/lib/api';
import { ensureResponse, serviceCall } from '@/services/base/BaseService';
import type {
  Automation,
  AutomationCreateRequest,
  AutomationUpdateRequest,
} from '@/types/automation.types';

async function listAutomations(): Promise<Automation[]> {
  return serviceCall(async () => {
    const response = await apiClient.get<Automation[]>('/automations');
    return ensureResponse(response, 'Failed to load automations');
  });
}

async function createAutomation(data: AutomationCreateRequest): Promise<Automation> {
  return serviceCall(async () => {
    const response = await apiClient.post<Automation>('/automations', data);
    return ensureResponse(response, 'Failed to create automation');
  });
}

async function updateAutomation(
  automationId: string,
  data: AutomationUpdateRequest,
): Promise<Automation> {
  return serviceCall(async () => {
    const response = await apiClient.patch<Automation>(`/automations/${automationId}`, data);
    return ensureResponse(response, 'Failed to update automation');
  });
}

async function deleteAutomation(automationId: string): Promise<void> {
  await serviceCall(async () => {
    await apiClient.delete(`/automations/${automationId}`);
  });
}

async function runAutomation(automationId: string): Promise<{ chat_id: string }> {
  return serviceCall(async () => {
    const response = await apiClient.post<{ chat_id: string }>(`/automations/${automationId}/run`);
    return ensureResponse(response, 'Failed to run automation');
  });
}

export const automationService = {
  listAutomations,
  createAutomation,
  updateAutomation,
  deleteAutomation,
  runAutomation,
};
