import { Fragment, useEffect, useMemo } from 'react';
import { useMatch } from 'react-router-dom';
import { PanelRight } from 'lucide-react';
import clsx from 'clsx';
import { Button } from '@/components/ui/primitives/Button/Button';
import { Tooltip } from '@/components/ui/Tooltip/Tooltip';
import {
  ALL_COMMANDS,
  formatShortcut,
  SESSION_PANEL_SHORTCUT,
} from '@/components/ui/command-menu/commandRegistry';
import { isSessionPanelShown, useUIStore } from '@/store/uiStore';
import { useIsMobile } from '@/hooks/useIsMobile';
import { isCoarsePointer } from '@/utils/device';
import { useSettingsQuery } from '@/hooks/queries/useSettingsQueries';
import { useChatArtifactsQuery } from '@/hooks/queries/useArtifactQueries';
import { artifactTileFor, latestArtifact } from '@/hooks/useOpenArtifact';
import { activeSplitSlot, VIEW_ICONS, VIEW_LABELS, viewTypeToTileId } from '@/utils/tileHelpers';
import type { ViewType } from '@/types/ui.types';

const ArtifactIcon = VIEW_ICONS.artifact;
import styles from './ViewSwitcher.module.scss';

type SwitchableView = Exclude<ViewType, 'agent'>;

const SWITCHABLE_VIEWS: SwitchableView[] = ['diff', 'editor', 'terminal', 'browser'];

// Same shortcut bindings as the command palette so tooltips can't drift.
const VIEW_SHORTCUTS = new Map<ViewType, string>(
  ALL_COMMANDS.flatMap((cmd) =>
    cmd.type === 'view' && cmd.shortcut ? [[cmd.id, formatShortcut(cmd.shortcut)] as const] : [],
  ),
);

const SESSION_PANEL_TOOLTIP = `Session panel · ${formatShortcut(SESSION_PANEL_SHORTCUT)}`;

function viewTooltip(view: SwitchableView): string {
  const shortcut = VIEW_SHORTCUTS.get(view);
  const label = shortcut ? `${VIEW_LABELS[view]} · ${shortcut}` : VIEW_LABELS[view];
  return `${label} · ⇧-click or right-click to split`;
}

export function ViewSwitcher() {
  const activeAgentTile = useUIStore((s) => s.activeAgentTile);
  const splitChatIds = useUIStore((s) => s.splitChatIds);
  const visibleLayout = useUIStore((s) => s.visibleLayout);
  const sessionPanelShown = useUIStore(isSessionPanelShown);
  const isLandingPage = useMatch('/');
  const routeChatId = useMatch('/chat/:chatId')?.params.chatId;
  const isMobile = useIsMobile();
  // Shortcut and split hints mean nothing on a phone, and a tap leaves the CSS
  // tooltip stuck open; the buttons keep their aria-labels.
  const showTooltips = !isMobile && !isCoarsePointer();
  // Hidden when the server runs no box browsers; shown until settings load.
  const browserAvailable = useSettingsQuery().data?.browser_available !== false;
  const views = browserAvailable
    ? SWITCHABLE_VIEWS
    : SWITCHABLE_VIEWS.filter((view) => view !== 'browser');

  // Active state matches the tile toggleView() will act on (focused split pane).
  const slot = activeSplitSlot(activeAgentTile, splitChatIds);
  // Highlight = on screen; background-mounted views stay unlit (click surfaces).
  const visibleSet = useMemo(() => new Set(visibleLayout.flat()), [visibleLayout]);

  return (
    <div className={styles['view-switcher']}>
      {views.map((view) => {
        const Icon = VIEW_ICONS[view];
        const isActive = visibleSet.has(viewTypeToTileId(view, slot));
        const button = (
          <Button
            variant="unstyled"
            // Left-click toggles full; shift/right-click splits beside the active pane.
            onClick={(e) => {
              if (e.shiftKey && !isMobile) {
                useUIStore.getState().addViewToSplit(view, 'row');
                return;
              }
              useUIStore.getState().toggleView(view, true);
            }}
            onContextMenu={(e) => {
              // Splitting is a desktop layout; leave the native long-press menu
              // alone on mobile, where the workspace is single-pane anyway.
              if (isMobile) return;
              e.preventDefault();
              useUIStore.getState().addViewToSplit(view, 'row');
            }}
            className={styles['view-button']}
            aria-label={`Toggle ${VIEW_LABELS[view]} view`}
            aria-pressed={isActive}
          >
            <Icon className={styles.icon} />
          </Button>
        );
        return showTooltips ? (
          <Tooltip key={view} content={viewTooltip(view)} position="bottom-end">
            {button}
          </Tooltip>
        ) : (
          <Fragment key={view}>{button}</Fragment>
        );
      })}
      {/* Same pane-scoping as the views: the focused split chat's artifacts. */}
      {(slot ? splitChatIds[slot - 1] : routeChatId) && !isMobile && (
        <ArtifactButton
          chatId={(slot ? splitChatIds[slot - 1] : routeChatId) as string}
          showTooltip={showTooltips}
        />
      )}
      {!isLandingPage && !isMobile && (
        <Tooltip content={SESSION_PANEL_TOOLTIP} position="bottom-end">
          <Button
            variant="unstyled"
            onClick={() => useUIStore.getState().toggleSessionPanel()}
            className={styles['view-button']}
            aria-label="Toggle session panel"
            aria-pressed={sessionPanelShown}
          >
            <PanelRight className={styles.icon} />
          </Button>
        </Tooltip>
      )}
    </div>
  );
}

// Only there once the chat has published an artifact. Opens it beside the
// chat; a dot marks a version the user hasn't had on screen yet.
function ArtifactButton({ chatId, showTooltip }: { chatId: string; showTooltip: boolean }) {
  const { data: artifacts } = useChatArtifactsQuery(chatId);
  const newest = latestArtifact(artifacts)?.updated_at;
  const seen = useUIStore((s) => s.artifactSeenByChat[chatId]);
  const isActive = useUIStore((s) =>
    s.visibleLayout.flat().includes(artifactTileFor(chatId, s.splitChatIds)),
  );
  // What's there on first load counts as seen; only later edits get the dot.
  useEffect(() => {
    if (artifacts && seen === undefined) {
      useUIStore.getState().markArtifactsSeen(chatId, newest ?? '');
    }
  }, [artifacts, chatId, newest, seen]);
  if (!artifacts?.length) return null;
  const unseen = !isActive && seen !== undefined && !!newest && newest > seen;

  const button = (
    <Button
      variant="unstyled"
      onClick={() => useUIStore.getState().toggleArtifact(chatId, null)}
      className={clsx(styles['view-button'], unseen && styles['view-button--dot'])}
      aria-label={unseen ? 'Toggle artifact view (updated)' : 'Toggle artifact view'}
      aria-pressed={isActive}
    >
      <ArtifactIcon className={styles.icon} />
    </Button>
  );
  return showTooltip ? (
    <Tooltip content={artifacts.length > 1 ? 'Artifacts' : 'Artifact'} position="bottom-end">
      {button}
    </Tooltip>
  ) : (
    button
  );
}
