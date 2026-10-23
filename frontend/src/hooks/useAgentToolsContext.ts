import { AgentToolsContext } from '@/contexts/AgentToolsContext';
import { createContextHook } from '@/hooks/createContextHook';

// Agent tools only render under the message renderer or a nested agent subtree, so a missing
// provider is a wiring bug rather than an empty-state case.
export const useAgentToolsContext = createContextHook(
  AgentToolsContext,
  'useAgentToolsContext',
  'AgentToolsContext',
);
