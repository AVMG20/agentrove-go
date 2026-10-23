// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { BoxInfo, CreateWorkspaceRequest } from '@/types/workspace.types';

const h = vi.hoisted(() => ({
  box: undefined as Partial<BoxInfo> | undefined,
  token: null as string | null,
  mutateAsync: vi.fn<(req: CreateWorkspaceRequest) => Promise<{ id: string }>>(),
}));

vi.mock('react-hot-toast', () => ({ default: { success: vi.fn(), error: vi.fn() } }));
vi.mock('@/hooks/queries/useWorkspaceQueries', () => ({
  useBoxQuery: () => ({ data: h.box }),
  useCreateWorkspaceMutation: () => ({ mutateAsync: h.mutateAsync, isPending: false }),
}));
vi.mock('@/hooks/queries/useSettingsQueries', () => ({
  useSettingsQuery: () => ({
    data: { github_personal_access_token: h.token },
    isSuccess: true,
  }),
}));
vi.mock('@/hooks/queries/useGitHubQueries', () => ({
  useGitHubReposQuery: () => ({ data: undefined, isLoading: false }),
}));

import { NewProject } from './NewProject';

function renderNewProject(onCreated = vi.fn()) {
  render(
    <MemoryRouter>
      <NewProject onCreated={onCreated} />
    </MemoryRouter>,
  );
  return onCreated;
}

beforeEach(() => {
  h.box = { available: true, host_available: false };
  h.token = null;
  h.mutateAsync.mockReset();
  h.mutateAsync.mockResolvedValue({ id: 'ws-new' });
});

afterEach(cleanup);

describe('NewProject', () => {
  it('creates an empty project in the box', async () => {
    const onCreated = renderNewProject();
    fireEvent.click(screen.getByText('Empty project'));
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: 'site' } });
    fireEvent.click(screen.getByText('Create project'));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('ws-new'));
    expect(h.mutateAsync).toHaveBeenCalledWith({
      name: 'site',
      source_type: 'empty',
      sandbox_provider: 'docker',
    });
  });

  it('clones by URL with the repo as the name and hints at a token', async () => {
    renderNewProject();
    fireEvent.click(screen.getByText('Clone a GitHub repo'));
    expect(screen.getByText(/to clone private repos and open PRs/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Repository URL'), {
      target: { value: 'https://github.com/org/my-app.git' },
    });
    fireEvent.click(screen.getByText('Clone'));
    await waitFor(() =>
      expect(h.mutateAsync).toHaveBeenCalledWith({
        name: 'my-app',
        source_type: 'git',
        git_url: 'https://github.com/org/my-app.git',
        sandbox_provider: 'docker',
      }),
    );
  });

  it('runs projects on the host when the server has no Docker', async () => {
    h.box = { available: false };
    renderNewProject();
    fireEvent.click(screen.getByText('Empty project'));
    fireEvent.click(screen.getByText('Create project'));
    await waitFor(() =>
      expect(h.mutateAsync).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Untitled', sandbox_provider: 'host' }),
      ),
    );
  });

  it('offers local folders only where the server allows the host provider', async () => {
    renderNewProject();
    expect(screen.queryByText('Open a folder on this computer')).toBeNull();
    cleanup();

    h.box = { available: true, host_available: true };
    renderNewProject();
    fireEvent.click(screen.getByText('Open a folder on this computer'));
    fireEvent.change(screen.getByLabelText('Folder path'), { target: { value: '~/code/app' } });
    fireEvent.click(screen.getByText('Open folder'));
    await waitFor(() =>
      expect(h.mutateAsync).toHaveBeenCalledWith({
        name: 'app',
        source_type: 'local',
        workspace_path: '~/code/app',
        sandbox_provider: 'host',
      }),
    );
  });
});
