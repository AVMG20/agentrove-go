import { memo, useCallback, useMemo, useState, type ReactNode } from 'react';
import clsx from 'clsx';
import { Star } from 'lucide-react';
import { Button } from '@/components/ui/primitives/Button/Button';
import { FloatingTooltip } from '@/components/ui/FloatingTooltip/FloatingTooltip';
import { Dropdown, type DropdownPosition } from '@/components/ui/primitives/Dropdown/Dropdown';
import type { DropdownItemType } from '@/components/ui/primitives/Dropdown/Dropdown';
import { useAuthStore } from '@/store/authStore';
import { useModelStore } from '@/store/modelStore';
import { useModelsQuery } from '@/hooks/queries/useModelQueries';
import { useProfilesQuery } from '@/hooks/queries/useProfileQueries';
import { PROVIDERS, PROVIDER_NAMES } from '@/utils/profiles';
import { useIsSplitMode } from '@/hooks/useIsSplitMode';
import { ProviderIcon } from '@/components/ui/icons/ProviderIcon';
import { providerIconComponent } from '@/components/ui/icons/providerIcons';
import { formatNumberCompact } from '@/utils/format';
import type { AgentKind, Model } from '@/types/chat.types';
import styles from './ModelSelector.module.scss';

export interface ModelSelectorProps {
  selectedModelId: string;
  onModelChange: (modelId: string) => void;
  // Whose models to offer: the thread's provider, or 'enabled' for every
  // provider the user has an account of (a rail switches between them).
  agentKind?: AgentKind | 'enabled';
  dropdownPosition?: DropdownPosition;
  disabled?: boolean;
  compact?: boolean;
  variant?: 'default' | 'text';
  dropdownAlign?: 'left' | 'right';
}

export const ModelSelector = memo(function ModelSelector({
  selectedModelId,
  onModelChange,
  agentKind = 'claude',
  dropdownPosition = 'bottom',
  dropdownAlign,
  disabled = false,
  compact,
  variant = 'default',
}: ModelSelectorProps) {
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const isSplitMode = useIsSplitMode();
  // Read-only models query — useModelSelection would run the per-chat defaulting
  // effect and persist models[0] before the owner derives the model from history.
  const multi = agentKind === 'enabled';
  const { data: models = [], isLoading } = useModelsQuery(
    { enabled: isAuthenticated },
    multi ? 'all' : agentKind,
  );
  const { data: profiles } = useProfilesQuery({ enabled: isAuthenticated && multi });
  const favoriteModelIds = useModelStore((state) => state.favoriteModelIds);

  // Providers with an account (all of them before any account exists).
  const providers = useMemo<AgentKind[]>(() => {
    if (!multi) return [agentKind];
    const have = new Set(profiles?.map((p) => p.provider));
    const list = PROVIDERS.filter((p) => have.has(p));
    return list.length > 0 ? list : PROVIDERS;
  }, [multi, agentKind, profiles]);

  const filteredModels = useMemo(
    () =>
      models.filter(
        (m) =>
          providers.includes(m.agent_kind) &&
          (!m.legacy || m.model_id === selectedModelId || favoriteModelIds.includes(m.model_id)),
      ),
    [models, providers, selectedModelId, favoriteModelIds],
  );

  const favoriteIdSet = useMemo(() => new Set(favoriteModelIds), [favoriteModelIds]);
  const selectedModel = filteredModels.find((m) => m.model_id === selectedModelId);

  // The rail's tab: favorites or one provider; opens on the selected model's.
  type Tab = AgentKind | 'favorites';
  const [pickedTab, setTab] = useState<Tab | null>(null);
  const showRail = multi && providers.length > 1;
  const tab: Tab = showRail
    ? (pickedTab ?? selectedModel?.agent_kind ?? providers[0])
    : (providers[0] ?? 'claude');
  // Each opening starts on the selected model's provider again.
  const resetTab = useCallback((open: boolean) => {
    if (!open) setTab(null);
  }, []);
  const hasFavorites = filteredModels.some((m) => favoriteIdSet.has(m.model_id));

  const groupedItems = useMemo(() => {
    const visible = showRail
      ? filteredModels.filter((m) =>
          tab === 'favorites' ? favoriteIdSet.has(m.model_id) : m.agent_kind === tab,
        )
      : filteredModels;
    // Favorites ordered by when each model was starred, not alphabetically.
    const modelById = new Map(visible.map((m) => [m.model_id, m]));
    const favoriteModels = favoriteModelIds
      .map((id) => modelById.get(id))
      .filter((m): m is Model => m !== undefined);
    const restModels = visible.filter((m) => !favoriteIdSet.has(m.model_id));

    const items: DropdownItemType<Model>[] = favoriteModels.map((model) => ({
      type: 'item',
      data: model,
    }));
    if (favoriteModels.length > 0 && restModels.length > 0) items.push({ type: 'divider' });
    restModels.forEach((model) => items.push({ type: 'item', data: model }));
    return items;
  }, [filteredModels, favoriteModelIds, favoriteIdSet, showRail, tab]);

  if (isLoading) {
    return (
      <div className={styles.status}>
        <div className={styles['status-pill']} />
      </div>
    );
  }

  if (filteredModels.length === 0) {
    return (
      <div className={styles.status}>
        <span className={styles['status-text']}>No models</span>
      </div>
    );
  }

  const activeModel = selectedModel || filteredModels[0];
  const ProviderGlyph = providerIconComponent(activeModel.agent_kind ?? providers[0]);

  return (
    <Dropdown
      value={activeModel}
      items={groupedItems}
      getItemKey={(model) => model.model_id}
      getItemLabel={(model) => model.name}
      onSelect={(model) => onModelChange(model.model_id)}
      leftIcon={ProviderGlyph}
      keepIcon
      width={showRail ? '19rem' : '16rem'}
      dropdownPosition={dropdownPosition}
      disabled={disabled}
      compactOnMobile={compact ?? true}
      forceCompact={compact ?? isSplitMode}
      searchable
      searchPlaceholder="Filter..."
      searchVariant="underline"
      selectionStyle="accent"
      triggerVariant={variant}
      dropdownAlign={dropdownAlign}
      onOpenChange={resetTab}
      renderRail={
        showRail
          ? () => (
              <div className={styles.rail} role="tablist" aria-orientation="vertical">
                {hasFavorites && (
                  <RailTab
                    label="Favorites"
                    active={tab === 'favorites'}
                    onClick={() => setTab('favorites')}
                  >
                    <Star className={styles['rail-icon']} />
                  </RailTab>
                )}
                {providers.map((provider) => (
                  <RailTab
                    key={provider}
                    label={PROVIDER_NAMES[provider]}
                    active={tab === provider}
                    onClick={() => setTab(provider)}
                  >
                    <ProviderIcon provider={provider} className={styles['rail-icon']} />
                  </RailTab>
                ))}
              </div>
            )
          : undefined
      }
      renderItem={(model, isSelected) => {
        const isFavorite = favoriteIdSet.has(model.model_id);
        return (
          <div className={styles.item}>
            <ProviderIcon provider={model.agent_kind} className={styles['item-icon']} />
            <FloatingTooltip content={model.name} className={styles['item-tooltip']}>
              <span
                className={clsx(styles['item-label'], isSelected && styles['item-label--selected'])}
              >
                {model.name}
              </span>
            </FloatingTooltip>
            {model.context_window != null && model.context_window > 0 && (
              <span className={styles['item-context']}>
                {formatNumberCompact(model.context_window)}
              </span>
            )}
            <Button
              type="button"
              variant="unstyled"
              aria-label={isFavorite ? 'Remove from favorites' : 'Add to favorites'}
              aria-pressed={isFavorite}
              onClick={(event) => {
                event.stopPropagation();
                useModelStore.getState().toggleFavoriteModel(model.model_id);
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.stopPropagation();
                }
              }}
              className={clsx(
                styles['favorite-button'],
                isFavorite && styles['favorite-button--active'],
              )}
            >
              <Star
                className={clsx(
                  styles['favorite-icon'],
                  isFavorite && styles['favorite-icon--active'],
                )}
              />
            </Button>
          </div>
        );
      }}
    />
  );
});

function RailTab({
  label,
  active,
  onClick,
  children,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <FloatingTooltip content={label}>
      <button
        type="button"
        role="tab"
        aria-selected={active}
        aria-label={label}
        className={clsx(styles['rail-tab'], active && styles['rail-tab--active'])}
        onClick={onClick}
      >
        {children}
      </button>
    </FloatingTooltip>
  );
}
