import { describe, expect, it } from 'vitest';
import type { BoxInfo, Workspace } from '@/types/workspace.types';
import {
  boxSizeBreakdown,
  formatIdleDuration,
  projectPath,
  pruneSummary,
  summarizeBox,
  tildePath,
} from './box';

const box = (over: Partial<BoxInfo> = {}): BoxInfo => ({
  available: true,
  state: 'running',
  busy: false,
  idle_for_seconds: 0,
  idle_ttl_seconds: 1800,
  disk_bytes: null,
  home_bytes: null,
  projects_bytes: null,
  total_bytes: null,
  ...over,
});

const workspace = (over: Partial<Workspace> = {}): Workspace => ({
  id: 'ws',
  name: 'app',
  user_id: 'u',
  sandbox_id: 's',
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
  it('maps a project into the box', () => {
    expect(projectPath(workspace())).toBe('/home/user/projects/app-1a2b3c4d');
  });

  it('appends the chat worktree', () => {
    expect(projectPath(workspace(), '.worktrees/ab')).toBe(
      '/home/user/projects/app-1a2b3c4d/.worktrees/ab',
    );
  });
});

describe('tildePath', () => {
  it('shortens paths in the box home', () => {
    expect(tildePath('/home/user/projects/app')).toBe('~/projects/app');
    expect(tildePath('/home/user')).toBe('~');
  });

  it('leaves other paths alone', () => {
    expect(tildePath('/home/username/x')).toBe('/home/username/x');
    expect(tildePath('/opt/x')).toBe('/opt/x');
  });
});

describe('boxSizeBreakdown', () => {
  it('lists the known parts', () => {
    expect(boxSizeBreakdown(box({ disk_bytes: 1024 * 1024, projects_bytes: 2 * 1024 ** 3 }))).toBe(
      'System 1.0 MB · Projects 2.0 GB',
    );
    expect(boxSizeBreakdown(box())).toBe('');
  });
});

describe('pruneSummary', () => {
  it('says what was freed, removed and kept', () => {
    expect(
      pruneSummary({
        freed_bytes: 3 * 1024 ** 3,
        removed: ['api', 'web'],
        kept: [{ name: 'notes', reason: 'has uncommitted changes' }],
        box: box(),
      }),
    ).toBe(
      'Freed 3.0 GB.\n2 projects are cloned again when you next use them.\nKept notes: has uncommitted changes.',
    );
  });
});
