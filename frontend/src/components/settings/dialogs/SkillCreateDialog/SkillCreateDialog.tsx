import { useId, useState } from 'react';
import { Input } from '@/components/ui/primitives/Input/Input';
import { Label } from '@/components/ui/primitives/Label/Label';
import { Textarea } from '@/components/ui/primitives/Textarea/Textarea';
import { BaseModal } from '@/components/ui/shared/BaseModal/BaseModal';
import { DialogFooter } from '@/components/ui/shared/DialogFooter/DialogFooter';
import { DialogError } from '@/components/ui/shared/DialogError/DialogError';
import { skillService } from '@/services/skillService';
import { buildSkillFile } from './skillFile';
import styles from './SkillCreateDialog.module.scss';

// Matches the backend's skill name rule (the folder and the /name command).
const SKILL_NAME = /^[a-z0-9][a-z0-9-]{0,63}$/;

interface SkillCreateDialogProps {
  isOpen: boolean;
  // Without a project the skill is global: available in every project.
  workspaceId?: string;
  workspaceName?: string;
  onClose: () => void;
  onCreated: () => void | Promise<void>;
}

export function SkillCreateDialog({
  isOpen,
  workspaceId,
  workspaceName,
  onClose,
  onCreated,
}: SkillCreateDialogProps) {
  const titleId = useId();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [body, setBody] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const nameValid = SKILL_NAME.test(name);

  const close = () => {
    setName('');
    setDescription('');
    setBody('');
    setError(null);
    onClose();
  };

  const handleSave = async () => {
    if (!nameValid) {
      setError('Use lowercase letters, digits and hyphens, starting with a letter or digit');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await skillService.createSkill(
        { workspaceId, scope: workspaceId ? 'project' : 'user' },
        name,
        [
          {
            path: 'SKILL.md',
            content: buildSkillFile(name, description, body),
            is_binary: false,
          },
        ],
      );
      await onCreated();
      close();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create skill');
    } finally {
      setSaving(false);
    }
  };

  return (
    <BaseModal
      isOpen={isOpen}
      onClose={close}
      size="4xl"
      className={styles.dialog}
      ariaLabelledBy={titleId}
    >
      <div className={styles.body}>
        <h3 id={titleId} className={styles.title}>
          {workspaceId ? `New skill in ${workspaceName ?? 'this project'}` : 'New global skill'}
        </h3>

        <DialogError error={error} className={styles.error} />

        <div className={styles.fields}>
          <div>
            <Label className={styles.label}>Name</Label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase().replace(/[\s_]+/g, '-'))}
              placeholder="release-notes"
              aria-label="Skill name"
              className={styles.input}
            />
            <p className={styles.hint}>
              Run it with /{name || 'name'}.{' '}
              {workspaceId
                ? 'Saved in the project’s .claude/skills.'
                : 'Available in every project.'}
            </p>
          </div>

          <div>
            <Label className={styles.label}>Description</Label>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Drafts release notes from the commits since the last tag"
              aria-label="Skill description"
              className={styles.input}
            />
            <p className={styles.hint}>Claude reads this to decide when to use the skill.</p>
          </div>

          <div>
            <Label className={styles.label}>Instructions</Label>
            <Textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="# Release notes&#10;&#10;1. List the commits since the last tag…"
              aria-label="Skill instructions"
              className={styles['content-textarea']}
              rows={15}
            />
          </div>
        </div>

        <DialogFooter
          onCancel={close}
          onSave={() => void handleSave()}
          saveLabel="Create skill"
          saving={saving}
          disabled={!name || !description.trim() || !body.trim() || saving}
        />
      </div>
    </BaseModal>
  );
}
