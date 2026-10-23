import { useId, useState, type KeyboardEvent } from 'react';
import clsx from 'clsx';
import { ChevronDown, Lock } from 'lucide-react';
import { Input } from '@/components/ui/primitives/Input/Input';
import { useGitHubReposQuery } from '@/hooks/queries/useGitHubQueries';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { useDropdown } from '@/hooks/useDropdown';
import type { GitHubRepo } from '@/types/github.types';
import styles from './GitHubRepoPicker.module.scss';

interface GitHubRepoPickerProps {
  value: string;
  onChange: (repo: string) => void;
  hasError?: boolean;
}

// owner/repo field that lists the user's repos (same source as the clone-repo
// picker) once a GitHub token is set; typing still accepts any repo.
export function GitHubRepoPicker({ value, onChange, hasError }: GitHubRepoPickerProps) {
  const { data: settings } = useSettingsQuery({ enabled: true });
  const hasGitHubToken = Boolean(settings?.github_personal_access_token);
  const { isOpen, dropdownRef, setIsOpen } = useDropdown();
  const listId = useId();
  // Until the user types, the panel shows their recent repos rather than
  // searching for the already-selected name.
  const [search, setSearch] = useState<string | null>(null);
  const debouncedSearch = useDebouncedValue(search ?? '', 300);
  const [highlighted, setHighlighted] = useState(0);

  const showPanel = isOpen && hasGitHubToken;
  const { data, isLoading, isError } = useGitHubReposQuery(
    search === null ? '' : debouncedSearch.trim(),
    showPanel,
  );
  const repos = data?.items ?? [];

  const open = () => {
    setSearch(null);
    setHighlighted(0);
    setIsOpen(true);
  };

  const select = (repo: GitHubRepo) => {
    onChange(repo.full_name);
    setIsOpen(false);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (!showPanel) {
      if (event.key === 'ArrowDown' && hasGitHubToken) {
        event.preventDefault();
        open();
      }
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      if (!repos.length) return;
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setHighlighted((index) => (index + step + repos.length) % repos.length);
    } else if (event.key === 'Enter') {
      const repo = repos[highlighted];
      if (repo) {
        event.preventDefault();
        select(repo);
      }
    } else if (event.key === 'Escape' || event.key === 'Tab') {
      if (event.key === 'Escape') event.preventDefault();
      setIsOpen(false);
    }
  };

  if (!hasGitHubToken) {
    return (
      <>
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="owner/repo"
          className={styles.input}
          hasError={hasError}
        />
        <p className={styles.hint}>
          Add a GitHub token in Settings → General to pick from your repositories.
        </p>
      </>
    );
  }

  return (
    <div className={styles.picker} ref={dropdownRef}>
      <Input
        role="combobox"
        aria-expanded={showPanel}
        aria-controls={listId}
        aria-autocomplete="list"
        aria-activedescendant={
          showPanel && repos[highlighted] ? `${listId}-${highlighted}` : undefined
        }
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setSearch(e.target.value);
          setHighlighted(0);
          setIsOpen(true);
        }}
        onFocus={open}
        onClick={() => !isOpen && open()}
        onKeyDown={handleKeyDown}
        placeholder="Search your repositories or type owner/repo"
        autoComplete="off"
        spellCheck={false}
        className={clsx(styles.input, styles['input--picker'])}
        // A half-typed search isn't an error yet.
        hasError={hasError && !showPanel}
      />
      <ChevronDown
        className={clsx(styles.chevron, showPanel && styles['chevron--open'])}
        aria-hidden="true"
      />

      {showPanel && (
        <div className={styles.panel}>
          <div id={listId} role="listbox" aria-label="Repositories" className={styles.items}>
            {isLoading ? (
              <p className={styles.status}>Loading repositories…</p>
            ) : isError ? (
              <p className={styles.status}>Couldn&apos;t load repositories</p>
            ) : !repos.length ? (
              <p className={styles.status}>
                {search ? 'No matching repositories' : 'No repositories'}
              </p>
            ) : (
              repos.map((repo, index) => (
                <div
                  key={repo.full_name}
                  id={`${listId}-${index}`}
                  role="option"
                  aria-selected={repo.full_name === value}
                  // Keep focus in the input so typing continues after a hover.
                  onMouseDown={(e) => e.preventDefault()}
                  onMouseEnter={() => setHighlighted(index)}
                  onClick={() => select(repo)}
                  className={clsx(styles.item, index === highlighted && styles['item--active'])}
                >
                  <div className={styles['item-header']}>
                    <span className={styles['item-name']}>{repo.full_name}</span>
                    {repo.private && <Lock className={styles['item-lock']} aria-label="Private" />}
                  </div>
                  {repo.description && (
                    <span className={styles['item-description']}>{repo.description}</span>
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}
