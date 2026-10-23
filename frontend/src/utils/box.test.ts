import { describe, expect, it } from 'vitest';
import type { BoxInfo, Workspace } from '@/types/workspace.types';
import { formatIdleDuration, projectPath, summarizeBox } from './box';

const box = (over: Partial<BoxInfo> = {}): BoxInfo => ({
  available: true,
  state: 'running',
  busy: false,
  idle_for_seconds: 0,
  idle_ttl_seconds: 1800,
  disk_bytes: null,
  host_available: false,
  ...over,
});

const workspace = (over: Partial<Workspace> = {}): Workspace => ({
  id: 'ws',
  name: 'app',
  user_id: 'u',
  sandbox_id: 's',
  sandbox_provider: 'docker',
  workspace_path: '/data/workspaces/u/app-1a2b3c4d',
  created_at: '',
  updated_at: '',
  chat_count: 0,
  last_chat_at: null,
  ...over,
});

describe('summarizeBox', () => {
  it('counts down to the idle stop, including time since the fetch', () => {
    expect(summarizeBox(box({ idle_for_seconds: 600 }), 60).detail).toBe('idles in 19m');
  });

  it('says Claude is working while a turn runs', () => {
    expect(summarizeBox(box({ busy: true }))).toMatchObject({
      tone: 'running',
      detail: 'Claude working',
    });
  });

  it('treats a missing box as stopped', () => {
    expect(summarizeBox(box({ state: 'none' }))).toMatchObject({
      tone: 'stopped',
      label: 'Box stopped',
    });
  });

  it('says stopping soon once the countdown has run out', () => {
    expect(summarizeBox(box({ idle_for_seconds: 1800 }), 5).detail).toBe('stopping soon');
  });
});

describe('formatIdleDuration', () => {
  it('rounds up to minutes and switches to hours', () => {
    expect(formatIdleDuration(30)).toBe('1m');
    expect(formatIdleDuration(3600)).toBe('1h');
    expect(formatIdleDuration(3900)).toBe('1h 5m');
  });
});

describe('projectPath', () => {
  it('maps a Docker project into the box', () => {
    expect(projectPath(workspace())).toBe('/home/user/projects/app-1a2b3c4d');
  });

  it('keeps a host path and appends the worktree', () => {
    expect(
      projectPath(
        workspace({ sandbox_provider: 'host', workspace_path: '/src/app' }),
        '.worktrees/ab',
      ),
    ).toBe('/src/app/.worktrees/ab');
  });
});
