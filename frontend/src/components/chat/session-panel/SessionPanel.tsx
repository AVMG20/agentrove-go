import { memo, useEffect, type ReactNode } from 'react';
import { AppWindow, Box, ExternalLink, Folder, GitBranch } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { useChatArtifactsQuery } from '@/hooks/queries/useArtifactQueries';
import { useModelMap } from '@/hooks/queries/useModelQueries';
import {
  useGitBranchesQuery,
  useGitDiffStatsQuery,
  useGitFetchBaseMutation,
} from '@/hooks/queries/useSandboxQueries';
import { useBranchPullRequests } from '@/hooks/queries/useGitHubQueries';
import { useBoxQuery } from '@/hooks/queries/useWorkspaceQueries';
import { useChatAgentKind } from '@/hooks/useChatAgentKind';
import { useChatSessionState } from '@/hooks/useChatSessionContext';
import { useUIStore } from '@/store/uiStore';
import type { Chat } from '@/types/chat.types';
import type { DiffMode, GitDiffTotals } from '@/types/sandbox.types';
import type { Workspace } from '@/types/workspace.types';
import { getRelativeTime } from '@/utils/date';
import { getContextUsageDisplay } from '@/utils/contextUsage';
import { openExternalUrl } from '@/utils/openExternal';
import { projectPath } from '@/utils/box';
import styles from './SessionPanel.module.scss';

// Dialogs and view toggles act on the focused pane; the panel describes the
// routed chat, so point them at the primary pane first.
function focusPrimaryAgent() {
  useUIStore.getState().focusTile('agent:primary');
}

function Card({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className={styles.card}>
      <header className={styles['card-header']}>
        <h3 className={styles['card-title']}>{title}</h3>
        {action}
      </header>
      {children}
    </section>
  );
}

function Row({ icon, children, hint }: { icon: ReactNode; children: ReactNode; hint?: string }) {
  return (
    <div className={styles.row}>
      <span className={styles['row-icon']}>{icon}</span>
      {hint ? (
        <FloatingTooltip content={hint} className={styles['row-text']}>
          {children}
        </FloatingTooltip>
      ) : (
        <span className={styles['row-text']}>{children}</span>
      )}
    </div>
  );
}

function ModelRow({
  chatId,
  sessionAgentKind,
  provider,
}: {
  chatId: string;
  sessionAgentKind: Chat['session_agent_kind'];
  provider: Chat['provider'];
}) {
  const { selectedModelId } = useChatSessionState();
  const modelMap = useModelMap();
  const agentKind = useChatAgentKind(chatId, sessionAgentKind, provider);
  const model = modelMap.get(selectedModelId);
  if (!selectedModelId) return null;
  return (
    <Row icon={agentKind ? <ProviderIcon provider={agentKind} className={styles.glyph} /> : null}>
      {model?.name ?? selectedModelId}
    </Row>
  );
}

function EnvironmentCard({
  chat,
  workspace,
  branch,
  children,
}: {
  chat: Chat;
  workspace: Workspace | undefined;
  branch: string | null;
  children?: ReactNode;
}) {
  const path = workspace ? projectPath(workspace, chat.worktree_cwd) : undefined;
  return (
    <Card title="Environment">
      <Row icon={<Box className={styles.glyph} />}>
        Your box
        <span className={styles.secondary}> · Docker</span>
      </Row>
      {workspace && (
        <Row icon={<Folder className={styles.glyph} />} hint={path}>
          {workspace.name}
          {path && <span className={styles.secondary}>{` · ${path}`}</span>}
        </Row>
      )}
      {branch && (
        <Row icon={<GitBranch className={styles.glyph} />} hint={branch}>
          {branch}
        </Row>
      )}
      {children}
    </Card>
  );
}

function ContextCard({ chat }: { chat: Chat }) {
  const { contextUsage } = useChatSessionState();
  if (!contextUsage) return null;
  const { percentage, tone, label, tooltip } = getContextUsageDisplay(contextUsage);
  return (
    <Card
      title="Context"
      action={
        <FloatingTooltip content={tooltip} className={styles['context-value']}>
          {label}
        </FloatingTooltip>
      }
    >
      <div
        className={styles['context-track']}
        role="progressbar"
        aria-label={`Context used in ${chat.title}`}
        aria-valuenow={Math.round(percentage)}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className={clsx(styles['context-fill'], styles[`context-fill--${tone}`])}
          style={{ width: `${percentage}%` }}
        />
      </div>
    </Card>
  );
}

// Only shown once the agent has published something from this chat.
function ArtifactsCard({ chatId }: { chatId: string }) {
  const { data: published } = useChatArtifactsQuery(chatId);
  if (!published || published.length === 0) return null;
  return (
    <Card title="Artifacts">
      {published.map((artifact) => (
        // The viewer is a standalone page — keep the chat open next to it.
        <a
          key={artifact.id}
          href={`/artifacts/${artifact.id}`}
          target="_blank"
          rel="noopener noreferrer"
          className={styles['thread-row']}
        >
          <span className={styles['row-icon']}>
            <AppWindow className={styles.glyph} />
          </span>
          <span className={styles['row-text']}>{artifact.title}</span>
          <span className={styles.timestamp}>{getRelativeTime(artifact.updated_at)}</span>
        </a>
      ))}
    </Card>
  );
}

function openDiff(chatId: string, mode: DiffMode) {
  focusPrimaryAgent();
  useUIStore.setState({ pendingDiffMode: { chatId, mode } });
  useUIStore.getState().toggleView('diff', false);
}

function ChangesRow({
  label,
  totals,
  onOpen,
  action,
}: {
  label: string;
  totals: GitDiffTotals;
  onOpen: () => void;
  action: ReactNode;
}) {
  return (
    <div className={styles['changes-row']}>
      <Button
        variant="unstyled"
        type="button"
        className={styles['diff-stats']}
        onClick={onOpen}
        aria-label={`Open ${label.toLowerCase()} diff`}
      >
        <span className={styles['changes-label']}>{label}</span>
        <span className={styles['changes-numbers']}>
          {(totals.additions > 0 || totals.deletions > 0) && (
            <>
              <span className={styles['stat-add']}>+{totals.additions}</span>
              <span className={styles['stat-del']}>−{totals.deletions}</span>
            </>
          )}
          <span className={styles.secondary}>
            {totals.files_changed} {totals.files_changed === 1 ? 'file' : 'files'}
          </span>
        </span>
      </Button>
      {action}
    </div>
  );
}

function ChangesCard({
  chatId,
  sandboxId,
  worktreeCwd,
  branch,
}: {
  chatId: string;
  sandboxId: string;
  worktreeCwd: string | undefined;
  branch: string;
}) {
  const { data: stats } = useGitDiffStatsQuery(sandboxId, worktreeCwd);
  const { mutate: fetchBase } = useGitFetchBaseMutation();
  // Fetching runs git in the box; just looking at a chat shouldn't wake a
  // stopped one, so wait until it runs anyway (the next turn).
  const { data: box } = useBoxQuery();
  const canFetch = box?.state === 'running';
  useEffect(() => {
    if (canFetch) fetchBase({ sandboxId, cwd: worktreeCwd });
  }, [fetchBase, sandboxId, worktreeCwd, canFetch]);
  const { openPR, mergedPR } = useBranchPullRequests(sandboxId, worktreeCwd, branch);
  if (!stats) return null;
  const hasUncommitted = stats.uncommitted.files_changed > 0;
  // A PR only carries committed work, so the PR action belongs to the branch row.
  const mergedAtHead = mergedPR && mergedPR.head.sha === stats.head_sha ? mergedPR : undefined;
  const linkedPR = openPR ?? mergedAtHead;
  const showBranch = stats.branch.files_changed > 0 || !!linkedPR;
  return (
    <Card title="Changes">
      {hasUncommitted && (
        <ChangesRow
          label="Uncommitted"
          totals={stats.uncommitted}
          onOpen={() => openDiff(chatId, 'all')}
          action={
            <Button
              variant="unstyled"
              type="button"
              className={styles['pr-button']}
              onClick={() => {
                focusPrimaryAgent();
                useUIStore.getState().setCreateCommitDialogOpen(true);
              }}
            >
              Commit
            </Button>
          }
        />
      )}
      {showBranch && (
        <ChangesRow
          label="Branch"
          totals={stats.branch}
          onOpen={() => openDiff(chatId, 'branch')}
          action={
            linkedPR ? (
              <Button
                variant="unstyled"
                type="button"
                className={styles['pr-button']}
                onClick={() => openExternalUrl(linkedPR.html_url)}
              >
                PR #{linkedPR.number}
                {!openPR && ' · Merged'}
                <ExternalLink className={styles['pr-icon']} />
              </Button>
            ) : (
              <Button
                variant="unstyled"
                type="button"
                className={styles['pr-button']}
                onClick={() => {
                  focusPrimaryAgent();
                  useUIStore.getState().setCreatePRDialogOpen(true);
                }}
              >
                Create PR
              </Button>
            )
          }
        />
      )}
      {!hasUncommitted && !showBranch && <p className={styles.empty}>No changes</p>}
    </Card>
  );
}

interface SessionPanelProps {
  chat: Chat;
  workspace: Workspace | undefined;
}

export const SessionPanel = memo(function SessionPanel({ chat, workspace }: SessionPanelProps) {
  const worktreeCwd = chat.worktree_cwd ?? undefined;
  const { data: branches } = useGitBranchesQuery(chat.sandbox_id, true, worktreeCwd);
  const branch = branches?.is_git_repo ? branches.current_branch : null;
  return (
    <aside className={styles.panel} aria-label="Session details">
      <EnvironmentCard chat={chat} workspace={workspace} branch={branch}>
        <ModelRow
          chatId={chat.id}
          sessionAgentKind={chat.session_agent_kind}
          provider={chat.provider}
        />
      </EnvironmentCard>
      <ContextCard chat={chat} />
      <ArtifactsCard chatId={chat.id} />
      {branch && (
        <ChangesCard
          chatId={chat.id}
          sandboxId={chat.sandbox_id}
          worktreeCwd={worktreeCwd}
          branch={branch}
        />
      )}
    </aside>
  );
});
