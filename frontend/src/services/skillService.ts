import { apiClient } from '@/lib/api';
import { ensureResponse, withAuth } from '@/services/base/BaseService';
import type { CustomSkill, SkillScope } from '@/types/user.types';
import { validateRequired } from '@/utils/validation';

// Where skills are read from: a project (its own skills plus the global and
// plugin ones it sees) or, with scope 'user' and no project, the global ones.
// A scope with a project targets that one copy of a skill.
export interface SkillLocation {
  workspaceId?: string;
  scope?: SkillScope;
}

export const GLOBAL_SKILLS: SkillLocation = { scope: 'user' };

function locationQuery({ workspaceId, scope }: SkillLocation): string {
  if (!workspaceId && scope !== 'user') validateRequired(workspaceId, 'Project');
  const params = new URLSearchParams();
  if (workspaceId) params.set('workspace_id', workspaceId);
  if (scope) params.set('scope', scope);
  return `?${params.toString()}`;
}

async function listSkills(location: SkillLocation): Promise<CustomSkill[]> {
  return withAuth(async () => {
    const response = await apiClient.get<CustomSkill[]>(`/skills${locationQuery(location)}`);
    return ensureResponse(response, 'Failed to fetch skills');
  });
}

export interface SkillFileEntry {
  path: string;
  content: string;
  is_binary: boolean;
}

interface SkillFilesResponse {
  name: string;
  files: SkillFileEntry[];
}

async function getSkillFiles(
  location: SkillLocation,
  source: string,
  skillName: string,
): Promise<SkillFileEntry[]> {
  validateRequired(source, 'Source');
  validateRequired(skillName, 'Skill name');

  return withAuth(async () => {
    const response = await apiClient.get<SkillFilesResponse>(
      `/skills/${source}/${encodeURIComponent(skillName)}/files${locationQuery(location)}`,
    );
    const data = ensureResponse(response, 'Failed to fetch skill files');
    return data.files;
  });
}

async function updateSkill(
  location: SkillLocation,
  source: string,
  skillName: string,
  files: SkillFileEntry[],
): Promise<CustomSkill> {
  validateRequired(source, 'Source');
  validateRequired(skillName, 'Skill name');

  return withAuth(async () => {
    const response = await apiClient.put<CustomSkill>(
      `/skills/${source}/${encodeURIComponent(skillName)}${locationQuery(location)}`,
      { files },
    );
    return ensureResponse(response, 'Failed to update skill');
  });
}

// Creates a skill in a project (scope 'project') or globally (scope 'user').
async function createSkill(
  location: SkillLocation & { scope: SkillScope },
  name: string,
  files: SkillFileEntry[],
): Promise<CustomSkill> {
  validateRequired(name, 'Skill name');

  return withAuth(async () => {
    const response = await apiClient.post<CustomSkill>(
      `/skills${locationQuery({ workspaceId: location.workspaceId, scope: location.workspaceId ? undefined : 'user' })}`,
      { source: 'claude', name, scope: location.scope, files },
    );
    return ensureResponse(response, 'Failed to create skill');
  });
}

async function deleteSkill(location: SkillLocation, source: string, skillName: string) {
  validateRequired(source, 'Source');
  validateRequired(skillName, 'Skill name');

  await withAuth(async () => {
    await apiClient.delete(
      `/skills/${source}/${encodeURIComponent(skillName)}${locationQuery(location)}`,
    );
  });
}

export const skillService = {
  listSkills,
  getSkillFiles,
  updateSkill,
  createSkill,
  deleteSkill,
};
