import { describe, it, expect } from 'vitest';
import { coerceThinkingMode } from '@/components/chat/thinking-mode-selector/thinkingModes';
import { buildAgentChatFields } from './chatRequest';
import type { Model } from '@/types/chat.types';
import type { Persona } from '@/types/user.types';
import type { PermissionMode } from '@/store/chatSettingsStore';

const model = (over: Partial<Model> & { model_id: string }): Model => ({
  name: over.model_id,
  agent_kind: 'claude',
  context_window: null,
  thinking_modes: [],
  ...over,
});

const raw = (over: Partial<Parameters<typeof buildAgentChatFields>[2]> = {}) => ({
  permissionMode: 'bypassPermissions' as const,
  thinkingMode: 'high',
  worktree: false,
  baseBranch: undefined,
  persona: 'Default',
  ...over,
});

describe('coerceThinkingMode', () => {
  it('uses the default for empty or unknown modes', () => {
    const sonnet = model({
      model_id: 'claude-sonnet-5',
      thinking_modes: ['low', 'medium', 'high', 'xhigh'],
    });
    expect(coerceThinkingMode('', undefined)).toBe('high');
    expect(coerceThinkingMode('unknown', sonnet)).toBe('high');
  });
});

describe('buildAgentChatFields', () => {
  it('coerces a stale permission mode from a removed agent to the default', () => {
    const fields = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ permissionMode: 'full-access' as PermissionMode }),
      [],
    );
    expect(fields.permission_mode).toBe('bypassPermissions');
  });

  it('keeps a valid permission and thinking mode for a claude model', () => {
    const map = new Map<string, Model>([
      [
        'claude-sonnet-5',
        model({
          model_id: 'claude-sonnet-5',
          thinking_modes: ['low', 'medium', 'high', 'xhigh', 'max'],
        }),
      ],
    ]);
    const fields = buildAgentChatFields(
      'claude-sonnet-5',
      map,
      raw({ permissionMode: 'plan', thinkingMode: 'max' }),
      [],
    );
    expect(fields.permission_mode).toBe('plan');
    expect(fields.thinking_mode).toBe('max');
  });

  it('clamps an unsupported thinking mode to the nearest lower tier', () => {
    // A model without a 'max' tier clamps to 'xhigh'.
    const map = new Map<string, Model>([
      [
        'claude-haiku-5',
        model({
          model_id: 'claude-haiku-5',
          thinking_modes: ['low', 'medium', 'high', 'xhigh'],
        }),
      ],
    ]);
    const fields = buildAgentChatFields('claude-haiku-5', map, raw({ thinkingMode: 'max' }), []);
    expect(fields.thinking_mode).toBe('xhigh');
  });

  it('maps worktree to true only when enabled, else undefined', () => {
    const on = buildAgentChatFields('claude-x', new Map(), raw({ worktree: true }), []);
    const off = buildAgentChatFields('claude-x', new Map(), raw({ worktree: false }), []);
    expect(on.worktree).toBe(true);
    expect(off.worktree).toBeUndefined();
  });

  it('sends base_branch only when a worktree is being created this turn', () => {
    const worktreeWithBase = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ worktree: true, baseBranch: 'develop' }),
      [],
    );
    const worktreeNoBase = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ worktree: true, baseBranch: undefined }),
      [],
    );
    const baseWithoutWorktree = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ worktree: false, baseBranch: 'develop' }),
      [],
    );
    expect(worktreeWithBase.base_branch).toBe('develop');
    expect(worktreeNoBase.base_branch).toBeUndefined();
    // A stale base can't leak onto a turn that isn't cutting a worktree.
    expect(baseWithoutWorktree.base_branch).toBeUndefined();
  });

  it('keeps a known custom persona and drops an unknown one back to Default', () => {
    const personas: Persona[] = [{ name: 'Reviewer', content: '' }];
    const known = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ persona: 'Reviewer' }),
      personas,
    );
    const unknown = buildAgentChatFields(
      'claude-x',
      new Map(),
      raw({ persona: 'Ghost' }),
      personas,
    );
    expect(known.selected_persona_name).toBe('Reviewer');
    expect(unknown.selected_persona_name).toBe('Default');
  });
});
