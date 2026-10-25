import type { CustomSkill } from '@/types/user.types';
import { Plus, Zap, Search, type LucideIcon } from 'lucide-react';
import { useCallback, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import toast from 'react-hot-toast';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import { Select } from '@/components/ui/primitives/Select/Select';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog/ConfirmDialog';
import { SkillCreateDialog } from '@/components/settings/dialogs/SkillCreateDialog/SkillCreateDialog';
import { SkillEditDialog } from '@/components/settings/dialogs/SkillEditDialog/SkillEditDialog';
import { SkillListItem } from '@/components/settings/SkillListItem/SkillListItem';
import { useWorkspacesList } from '@/hooks/queries/useWorkspaceQueries';
import { useSkillsQuery } from '@/hooks/queries/useSkillsQueries';
import { queryKeys } from '@/hooks/queries/queryKeys';
import { useSkillsFilter } from '@/hooks/useSkillsFilter';
import { GLOBAL_SKILLS, skillService, type SkillLocation } from '@/services/skillService';
import styles from './SkillsSettingsTab.module.scss';

interface EmptyStateProps {
  icon: LucideIcon;
  message: string;
}

// Stable [] so useSkillsFilter's memo doesn't rerun every render.
const EMPTY_SKILLS: CustomSkill[] = [];

// The selector's value for the global list (workspace ids are UUIDs).
const GLOBAL = 'global';

function EmptyState({ icon: Icon, message }: EmptyStateProps) {
  return (
    <div className={styles.empty}>
      <Icon className={styles['empty-icon']} />
      <p className={styles['empty-text']}>{message}</p>
    </div>
  );
}

export function SkillsSettingsTab() {
  const queryClient = useQueryClient();
  const workspaces = useWorkspacesList();
  const [selected, setSelected] = useState(GLOBAL);
  const workspace = workspaces.find((ws) => ws.id === selected);
  const workspaceId = workspace?.id;
  const location = useMemo<SkillLocation>(
    () => (workspaceId ? { workspaceId } : GLOBAL_SKILLS),
    [workspaceId],
  );

  const { data: skills, isLoading } = useSkillsQuery(location);
  const items = skills ?? EMPTY_SKILLS;
  const [editingSkill, setEditingSkill] = useState<CustomSkill | null>(null);
  const [deletingSkill, setDeletingSkill] = useState<CustomSkill | null>(null);
  const [creating, setCreating] = useState(false);

  const { searchQuery, setSearchQuery, filteredItems } = useSkillsFilter(items, selected);

  // A global skill shows up in every project's list and slash commands.
  const refresh = useCallback(async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: [queryKeys.skills] }),
      queryClient.invalidateQueries({
        predicate: (query) =>
          query.queryKey[0] === 'workspaces' && query.queryKey[2] === 'resources',
      }),
    ]);
  }, [queryClient]);

  const handleDelete = useCallback(
    async (skill: CustomSkill) => {
      try {
        await skillService.deleteSkill(
          { workspaceId, scope: skill.scope },
          skill.sources[0],
          skill.name,
        );
        toast.success(`Deleted ${skill.name}`);
      } catch (error) {
        toast.error(error instanceof Error ? error.message : 'Failed to delete skill');
      }
      await refresh();
    },
    [refresh, workspaceId],
  );

  return (
    <div>
      <div className={styles.header}>
        <div className={styles['header-top']}>
          <h2 className={styles.title}>Skills</h2>
          {items.length > 0 && (
            <span className={styles.count}>
              {filteredItems.length} of {items.length}
            </span>
          )}
        </div>
        <p className={styles.description}>
          {workspaceId
            ? 'Skills available in this project: its own, the global ones and those from plugins.'
            : 'Global skills are available in every project.'}
        </p>
      </div>

      <div className={styles.controls}>
        <div className={styles['search-wrap']}>
          <Search className={styles['search-icon']} />
          <Input
            variant="unstyled"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search skills…"
            aria-label="Search skills"
            className={styles['search-input']}
          />
        </div>
        <div className={styles['controls-end']}>
          <div className={styles['workspace-wrap']}>
            <Select
              value={workspaceId ?? GLOBAL}
              onChange={(e) => setSelected(e.target.value)}
              aria-label="Skills location"
            >
              <option value={GLOBAL}>Global</option>
              {workspaces.map((ws) => (
                <option key={ws.id} value={ws.id}>
                  {ws.name}
                </option>
              ))}
            </Select>
          </div>
          <Button
            type="button"
            variant="outline"
            onClick={() => setCreating(true)}
            className={styles['new-button']}
          >
            <Plus size={14} />
            New skill
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className={styles['loading-wrap']}>
          <p className={styles['loading-text']}>Loading skills…</p>
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          icon={Zap}
          message={workspaceId ? 'No skills in this project' : 'No global skills yet'}
        />
      ) : filteredItems.length === 0 ? (
        <EmptyState icon={Search} message="No skills match your search" />
      ) : (
        <div className={styles.list}>
          {filteredItems.map((skill) => (
            <SkillListItem
              key={`${skill.scope}/${skill.name}/${skill.sources.join(',')}`}
              skill={skill}
              showScope={!!workspaceId}
              onEdit={setEditingSkill}
              onDelete={setDeletingSkill}
            />
          ))}
        </div>
      )}

      <SkillEditDialog
        isOpen={editingSkill !== null}
        location={location}
        skill={editingSkill}
        onClose={() => setEditingSkill(null)}
        onSaved={refresh}
      />
      <SkillCreateDialog
        isOpen={creating}
        workspaceId={workspaceId}
        workspaceName={workspace?.name}
        onClose={() => setCreating(false)}
        onCreated={refresh}
      />
      <ConfirmDialog
        isOpen={deletingSkill !== null}
        onClose={() => setDeletingSkill(null)}
        onConfirm={() => deletingSkill && void handleDelete(deletingSkill)}
        title="Delete skill"
        message={
          deletingSkill?.scope === 'user'
            ? `Delete the global skill "${deletingSkill.name}"? It disappears from every project. This cannot be undone.`
            : `Delete "${deletingSkill?.name}" from this project? This cannot be undone.`
        }
        confirmLabel="Delete"
      />
    </div>
  );
}
