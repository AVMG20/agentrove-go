import { describe, expect, it } from 'vitest';
import type { Chat } from '@/types/chat.types';
import type { Workspace } from '@/types/workspace.types';
import { defaultWorkspaceId } from './defaultWorkspace';

const project = (id: string, lastChatAt: string | null) =>
  ({ id, last_chat_at: lastChatAt }) as Workspace;
const chat = (updatedAt: string, noProject: boolean) =>
  ({ updated_at: updatedAt, no_project: noProject }) as Chat;

describe('defaultWorkspaceId', () => {
  const projects = [project('a', '2026-01-02T00:00:00Z'), project('b', '2026-01-01T00:00:00Z')];

  it('picks the most recently active project', () => {
    expect(defaultWorkspaceId(projects, [chat('2026-01-02T00:00:00Z', false)])).toBe('a');
    expect(defaultWorkspaceId(projects, [])).toBe('a');
  });

  it('picks no project when the latest thread had none', () => {
    expect(defaultWorkspaceId(projects, [chat('2026-01-03T00:00:00Z', true)])).toBeNull();
    expect(
      defaultWorkspaceId([project('a', null)], [chat('2026-01-01T00:00:00Z', true)]),
    ).toBeNull();
  });

  it('keeps the project when a pinned project thread is newer', () => {
    expect(defaultWorkspaceId(projects, [chat('2026-01-01T12:00:00Z', true)])).toBe('a');
  });

  it('picks no project without projects', () => {
    expect(defaultWorkspaceId([], [])).toBeNull();
  });
});
