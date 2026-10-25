// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { skillService } from '@/services/skillService';
import type { CustomSkill } from '@/types/user.types';
import { SkillsSettingsTab } from './SkillsSettingsTab';

vi.mock('@/services/skillService', () => ({
  GLOBAL_SKILLS: { scope: 'user' },
  skillService: {
    listSkills: vi.fn(),
    getSkillFiles: vi.fn(),
    updateSkill: vi.fn(),
    createSkill: vi.fn(),
    deleteSkill: vi.fn(),
  },
}));

vi.mock('@/hooks/queries/useWorkspaceQueries', () => ({
  useWorkspacesList: () => [{ id: 'ws1', name: 'gomail' }],
}));

const skill = (name: string, scope: CustomSkill['scope']): CustomSkill => ({
  name,
  description: `${name} skill`,
  size_bytes: 100,
  file_count: 1,
  sources: ['claude'],
  scope,
  read_only: scope === 'plugin',
});

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('SkillsSettingsTab', () => {
  it('lists global skills first and a project on request', async () => {
    const list = vi.mocked(skillService.listSkills);
    list.mockImplementation(async (location) =>
      location.workspaceId
        ? [skill('deploy', 'project'), skill('notes', 'user'), skill('artifacts', 'plugin')]
        : [skill('notes', 'user')],
    );
    render(<SkillsSettingsTab />, { wrapper });

    await screen.findByText('notes');
    expect(list).toHaveBeenCalledWith({ scope: 'user' });
    expect(screen.queryByText('global')).toBeNull();

    fireEvent.change(screen.getByRole('combobox', { name: 'Skills location' }), {
      target: { value: 'ws1' },
    });
    await screen.findByText('deploy');
    expect(list).toHaveBeenLastCalledWith({ workspaceId: 'ws1' });
    expect(screen.getByText('global')).toBeTruthy();
    expect(screen.getByText('plugin')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Delete artifacts' })).toBeNull();
  });

  it('creates a global skill with a SKILL.md', async () => {
    vi.mocked(skillService.listSkills).mockResolvedValue([]);
    vi.mocked(skillService.createSkill).mockResolvedValue(skill('release-notes', 'user'));
    render(<SkillsSettingsTab />, { wrapper });

    await screen.findByText('No global skills yet');
    fireEvent.click(screen.getByRole('button', { name: /New skill/ }));
    fireEvent.change(screen.getByLabelText('Skill name'), { target: { value: 'Release notes' } });
    fireEvent.change(screen.getByLabelText('Skill description'), {
      target: { value: 'Drafts release notes' },
    });
    fireEvent.change(screen.getByLabelText('Skill instructions'), {
      target: { value: 'List the commits.' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create skill' }));

    await waitFor(() =>
      expect(skillService.createSkill).toHaveBeenCalledWith(
        { workspaceId: undefined, scope: 'user' },
        'release-notes',
        [
          {
            path: 'SKILL.md',
            content:
              '---\nname: release-notes\ndescription: "Drafts release notes"\n---\n\nList the commits.\n',
            is_binary: false,
          },
        ],
      ),
    );
  });

  it('deletes a skill after confirming', async () => {
    vi.mocked(skillService.listSkills).mockResolvedValue([skill('notes', 'user')]);
    vi.mocked(skillService.deleteSkill).mockResolvedValue();
    render(<SkillsSettingsTab />, { wrapper });

    fireEvent.click(await screen.findByRole('button', { name: 'Delete notes' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() =>
      expect(skillService.deleteSkill).toHaveBeenCalledWith(
        { workspaceId: undefined, scope: 'user' },
        'claude',
        'notes',
      ),
    );
  });
});
