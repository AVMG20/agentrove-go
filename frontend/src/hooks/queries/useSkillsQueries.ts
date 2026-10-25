import { useQuery } from '@tanstack/react-query';
import type { UseQueryOptions } from '@tanstack/react-query';
import { skillService, type SkillLocation } from '@/services/skillService';
import type { CustomSkill } from '@/types/user.types';
import { queryKeys } from './queryKeys';

export const useSkillsQuery = (
  location: SkillLocation | undefined,
  options?: Partial<UseQueryOptions<CustomSkill[]>>,
) => {
  return useQuery({
    queryKey: [queryKeys.skills, location?.workspaceId ?? 'global'],
    queryFn: () => skillService.listSkills(location!),
    enabled: !!location,
    ...options,
  });
};
