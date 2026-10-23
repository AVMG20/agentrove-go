import { useState, type ReactNode } from 'react';
import toast from 'react-hot-toast';
import { ArrowLeft, FolderPlus, Github, HardDrive, Loader2, Search } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Input } from '@/components/ui/primitives/Input/Input';
import { GitHubTokenHint } from '@/components/chat/github/GitHubTokenHint';
import { useBoxQuery, useCreateWorkspaceMutation } from '@/hooks/queries/useWorkspaceQueries';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import { useGitHubReposQuery } from '@/hooks/queries/useGitHubQueries';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import type { CreateWorkspaceRequest } from '@/types/workspace.types';
import { projectNameFrom } from '@/utils/projectName';
import { CreateWorkspaceGitHubRepoItem } from './CreateWorkspaceGitHubRepoItem';
import { CreateWorkspaceRepoListSkeleton } from './CreateWorkspaceRepoListSkeleton';
import styles from './NewProject.module.scss';

type Mode = 'choose' | 'git' | 'empty' | 'local';

// Production servers run as root and turn the host provider off; once the
// server says so, stop offering local folders.
const HOST_UNAVAILABLE_KEY = 'agentrove:host-unavailable';

function readHostUnavailable(): boolean {
  try {
    return localStorage.getItem(HOST_UNAVAILABLE_KEY) === '1';
  } catch {
    return false;
  }
}

function rememberHostUnavailable() {
  try {
    localStorage.setItem(HOST_UNAVAILABLE_KEY, '1');
  } catch {
    // Only costs a repeat of the error next time.
  }
}

function FormHeader({
  title,
  onBack,
  aside,
  disabled,
}: {
  title: string;
  onBack: () => void;
  aside?: ReactNode;
  disabled?: boolean;
}) {
  return (
    <div className={styles['form-header']}>
      <Button
        variant="unstyled"
        type="button"
        onClick={onBack}
        disabled={disabled}
        className={styles['back-button']}
        aria-label="Back to project options"
      >
        <ArrowLeft className={styles['back-icon']} aria-hidden="true" />
      </Button>
      <span className={styles['form-title']}>{title}</span>
      {aside}
    </div>
  );
}

// Creates a project: clone a GitHub repo or start empty, both in the user's
// box. A folder on this computer is a quiet extra for self-hosted setups.
// onCreated selects the new project and closes the picker.
export function NewProject({
  onCreated,
  onNavigate,
}: {
  onCreated: (id: string) => void;
  onNavigate?: () => void;
}) {
  const { data: settings } = useSettingsQuery();
  const { data: box } = useBoxQuery();
  const createWorkspace = useCreateWorkspaceMutation();
  const hasGitHubToken = Boolean(settings?.github_personal_access_token);
  // Projects live in the box; without Docker the server runs them on the host.
  const provider = box?.available === false ? 'host' : 'docker';

  const [mode, setMode] = useState<Mode>('choose');
  const [name, setName] = useState('');
  const [gitUrl, setGitUrl] = useState('');
  const [localPath, setLocalPath] = useState('');
  const [pasteUrl, setPasteUrl] = useState(false);
  const [repoQuery, setRepoQuery] = useState('');
  const [cloning, setCloning] = useState<string | null>(null);
  const [hostUnavailable, setHostUnavailable] = useState(readHostUnavailable);
  const debouncedRepoQuery = useDebouncedValue(repoQuery, 300);
  const browseRepos = hasGitHubToken && !pasteUrl;

  const { data: reposData, isLoading: reposLoading } = useGitHubReposQuery(
    debouncedRepoQuery,
    mode === 'git' && browseRepos,
  );

  const reset = (next: Mode) => {
    setMode(next);
    setName('');
    setGitUrl('');
    setLocalPath('');
    setRepoQuery('');
    setPasteUrl(false);
  };

  const create = async (request: CreateWorkspaceRequest, label: string) => {
    try {
      const workspace = await createWorkspace.mutateAsync(request);
      onCreated(workspace.id);
      toast.success(label);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to create project';
      if (request.sandbox_provider === 'host' && /not available/i.test(message)) {
        rememberHostUnavailable();
        setHostUnavailable(true);
        reset('choose');
        toast.error('This server can’t open folders on its own computer');
        return;
      }
      toast.error(message);
    } finally {
      setCloning(null);
    }
  };

  const cloneRepo = (url: string, repoName: string, label = repoName) => {
    setCloning(label);
    void create(
      { name: repoName, source_type: 'git', git_url: url, sandbox_provider: provider },
      'Repository cloned',
    );
  };

  const cloneFromUrl = () => {
    const url = gitUrl.trim();
    if (!url) {
      toast.error('Enter a repository URL');
      return;
    }
    // "org/repo" reads better than the whole URL while it clones.
    const label = url.replace(/^https:\/\/(www\.)?github\.com\//, '').replace(/\.git$/, '');
    cloneRepo(url, projectNameFrom(url) || 'project', label);
  };

  const createEmpty = () => {
    void create(
      { name: name.trim() || 'Untitled', source_type: 'empty', sandbox_provider: provider },
      'Project created',
    );
  };

  const openLocal = () => {
    const path = localPath.trim();
    if (!path) {
      toast.error('Enter a folder path');
      return;
    }
    void create(
      {
        name: name.trim() || projectNameFrom(path) || 'project',
        source_type: 'local',
        workspace_path: path,
        sandbox_provider: 'host',
      },
      'Folder opened',
    );
  };

  const isPending = createWorkspace.isPending;

  if (mode === 'choose') {
    return (
      <div className={styles.choose}>
        <div className={styles.options}>
          <Button
            variant="unstyled"
            type="button"
            onClick={() => setMode('git')}
            className={styles.option}
          >
            <Github className={styles['option-icon']} aria-hidden="true" />
            <span className={styles['option-title']}>Clone a GitHub repo</span>
            <span className={styles['option-description']}>
              {hasGitHubToken ? 'Pick one of your repos' : 'Paste a repository URL'}
            </span>
          </Button>
          <Button
            variant="unstyled"
            type="button"
            onClick={() => setMode('empty')}
            className={styles.option}
          >
            <FolderPlus className={styles['option-icon']} aria-hidden="true" />
            <span className={styles['option-title']}>Empty project</span>
            <span className={styles['option-description']}>Start from scratch</span>
          </Button>
        </div>
        {!hostUnavailable && (
          <Button
            variant="unstyled"
            type="button"
            onClick={() => setMode('local')}
            className={styles['local-link']}
          >
            <HardDrive className={styles['local-icon']} aria-hidden="true" />
            Open a folder on this computer
          </Button>
        )}
      </div>
    );
  }

  if (mode === 'empty') {
    return (
      <div className={styles.form}>
        <FormHeader title="Empty project" onBack={() => reset('choose')} />
        <Input
          variant="unstyled"
          name="project-name"
          aria-label="Project name"
          autoComplete="off"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !isPending) createEmpty();
          }}
          placeholder="Project name"
          autoFocus
          className={styles['text-input']}
        />
        <div className={styles.actions}>
          <Button type="button" size="sm" onClick={createEmpty} isLoading={isPending}>
            Create project
          </Button>
        </div>
      </div>
    );
  }

  if (mode === 'local') {
    return (
      <div className={styles.form}>
        <FormHeader title="Folder on this computer" onBack={() => reset('choose')} />
        <Input
          variant="unstyled"
          name="local-folder-path"
          aria-label="Folder path"
          autoComplete="off"
          spellCheck={false}
          value={localPath}
          onChange={(e) => setLocalPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !isPending) openLocal();
          }}
          placeholder="~/code/my-app"
          autoFocus
          className={clsx(styles['text-input'], styles['text-input--mono'])}
        />
        <p className={styles.helper}>
          Runs on this computer (host), not in your box. The folder stays where it is.
        </p>
        <div className={styles.actions}>
          <Button type="button" size="sm" onClick={openLocal} isLoading={isPending}>
            Open folder
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.form}>
      <FormHeader
        title="Clone a GitHub repo"
        onBack={() => reset('choose')}
        disabled={isPending}
        aside={
          hasGitHubToken &&
          !isPending && (
            <Button
              variant="unstyled"
              type="button"
              onClick={() => {
                setPasteUrl(!pasteUrl);
                setRepoQuery('');
                setGitUrl('');
              }}
              className={styles['mode-toggle']}
            >
              {pasteUrl ? 'Browse repos' : 'Paste URL'}
            </Button>
          )
        }
      />

      {cloning ? (
        <div className={styles.cloning} role="status">
          <Loader2 className={styles['cloning-icon']} aria-hidden="true" />
          <span className={styles['cloning-label']}>Cloning {cloning}…</span>
        </div>
      ) : browseRepos ? (
        <>
          <div className={styles['search-field']}>
            <Search className={styles['search-icon']} aria-hidden="true" />
            <Input
              variant="unstyled"
              type="search"
              name="repository-search"
              aria-label="Search repositories"
              autoComplete="off"
              spellCheck={false}
              value={repoQuery}
              onChange={(e) => setRepoQuery(e.target.value)}
              placeholder="Search your repositories…"
              autoFocus
              className={clsx(styles['text-input'], styles['text-input--search'])}
            />
          </div>
          <div className={styles['repo-list']}>
            {reposLoading ? (
              <CreateWorkspaceRepoListSkeleton />
            ) : !reposData?.items.length ? (
              <p className={styles['repo-empty']}>
                {debouncedRepoQuery ? 'No repositories found' : 'No repositories'}
              </p>
            ) : (
              <div className={styles['repo-items']}>
                {reposData.items.map((repo) => (
                  <CreateWorkspaceGitHubRepoItem
                    key={repo.full_name}
                    repo={repo}
                    onSelect={(url, repoName) => cloneRepo(url, repoName, repo.full_name)}
                    isCloning={isPending}
                  />
                ))}
              </div>
            )}
          </div>
        </>
      ) : (
        <>
          <Input
            variant="unstyled"
            type="url"
            name="git-repository-url"
            aria-label="Repository URL"
            autoComplete="off"
            spellCheck={false}
            value={gitUrl}
            onChange={(e) => setGitUrl(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !isPending) cloneFromUrl();
            }}
            placeholder="https://github.com/org/repo"
            autoFocus
            className={clsx(styles['text-input'], styles['text-input--mono'])}
          />
          <GitHubTokenHint onNavigate={onNavigate} />
          <div className={styles.actions}>
            <Button type="button" size="sm" onClick={cloneFromUrl} isLoading={isPending}>
              Clone
            </Button>
          </div>
        </>
      )}
    </div>
  );
}
