import { useModelStore } from '@/store/modelStore';
import type { AgentKind } from '@/types/chat.types';

export function useChatAgentKind(
  chatId: string,
  sessionAgentKind: AgentKind | null | undefined,
): AgentKind | null {
  // session_agent_kind arrives mid-stream/after complete; until then a locally picked model implies it.
  const hasStoredModel = useModelStore((s) => !!s.modelByChat[chatId]);
  return sessionAgentKind ?? (hasStoredModel ? 'claude' : null);
}
