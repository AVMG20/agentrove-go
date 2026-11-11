import { type ReactNode, type Ref } from 'react';
import { Search } from 'lucide-react';
import { Button } from '../primitives/Button/Button';
import { Input } from '../primitives/Input/Input';
import type { MenuMode } from './commandRegistry';
import styles from './CommandMenuInput.module.scss';

interface CommandMenuInputProps {
  mode: MenuMode;
  query: string;
  onQueryChange: (value: string) => void;
  inputRef: Ref<HTMLInputElement>;
  onBack: () => void;
  listId: string;
  activeDescendant: string | undefined;
  // After the field (the phone Cancel button).
  trailing?: ReactNode;
}

const PLACEHOLDERS: Partial<Record<MenuMode, string>> = {
  branches: 'Search branches...',
  themes: 'Search themes...',
  chats: 'Search chat titles...',
  messages: 'Search messages...',
  settings: 'Search settings...',
};

export function CommandMenuInput({
  mode,
  query,
  onQueryChange,
  inputRef,
  onBack,
  listId,
  activeDescendant,
  trailing,
}: CommandMenuInputProps) {
  return (
    <div className={styles['input-row']}>
      {(mode === 'branches' || mode === 'themes') && (
        <Button
          variant="unstyled"
          onMouseDown={(e) => e.preventDefault()}
          onClick={onBack}
          className={styles['input-back']}
        >
          {mode === 'branches' ? 'Branches' : 'Themes'}
        </Button>
      )}
      <Search className={styles['input-icon']} />
      <Input
        ref={inputRef}
        variant="unstyled"
        value={query}
        onChange={(e) => onQueryChange(e.target.value)}
        placeholder={PLACEHOLDERS[mode] ?? 'Search chats, messages, settings...'}
        className={styles['input-field']}
        role="combobox"
        aria-expanded="true"
        aria-controls={listId}
        aria-activedescendant={activeDescendant}
      />
      {trailing}
    </div>
  );
}
