import { coercePermissionMode } from '@/components/chat/permission-mode-selector/permissionModes';
import { coerceThinkingMode } from '@/components/chat/thinking-mode-selector/thinkingModes';
import { resolvePersona } from '@/utils/settings';
import type { ChatRequest, Model } from '@/types/chat.types';
import type { PermissionMode } from '@/store/chatSettingsStore';
import type { Persona } from '@/types/user.types';

interface RawChatSettings {
  permissionMode: PermissionMode;
  thinkingMode: string;
  worktree: boolean;
  baseBranch?: string;
  persona: string;
}

type AgentChatFields = Pick<
  ChatRequest,
  'permission_mode' | 'thinking_mode' | 'worktree' | 'base_branch' | 'selected_persona_name'
>;

// Shared coercion so every send path normalizes the raw toolbar settings the same way.
export function buildAgentChatFields(
  selectedModelId: string,
  modelMap: Map<string, Model>,
  raw: RawChatSettings,
  personas: Persona[],
): AgentChatFields {
  const model = modelMap.get(selectedModelId);
  return {
    permission_mode: coercePermissionMode(raw.permissionMode),
    thinking_mode: coerceThinkingMode(raw.thinkingMode, model),
    worktree: raw.worktree ? true : undefined,
    // Only meaningful when this turn creates a worktree; omit otherwise so a stale
    // base can't leak onto a turn that isn't cutting one.
    base_branch: raw.worktree && raw.baseBranch ? raw.baseBranch : undefined,
    selected_persona_name: resolvePersona(raw.persona, personas),
  };
}
