// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Model } from '@/types/chat.types';
import type { Profile } from '@/types/profile.types';
import type { DropdownItemType } from '@/components/ui/primitives/Dropdown/Dropdown';
import { useModelStore } from '@/store/modelStore';
import { ModelSelector } from './ModelSelector';

vi.mock('@/hooks/useIsSplitMode', () => ({ useIsSplitMode: () => false }));
const modelsQuery = vi.fn();
vi.mock('@/hooks/queries/useModelQueries', () => ({
  useModelsQuery: (_options: unknown, kind: string) => {
    modelsQuery(kind);
    return { data: models, isLoading: false };
  },
}));
let profiles: Partial<Profile>[] = [];
vi.mock('@/hooks/queries/useProfileQueries', () => ({
  useProfilesQuery: () => ({ data: profiles }),
}));
vi.mock('@/components/ui/primitives/Dropdown/Dropdown', () => ({
  Dropdown: ({
    items,
    value,
    renderRail,
  }: {
    items: DropdownItemType<Model>[];
    value: Model;
    renderRail?: () => ReactNode;
  }) => (
    <div>
      <span>{value.name}</span>
      {renderRail?.()}
      <div data-testid="choices">
        {items.map(
          (item) =>
            item.type === 'item' && <button key={item.data.model_id}>{item.data.model_id}</button>,
        )}
      </div>
    </div>
  ),
}));

let models: Model[] = [];
const claudeModels: Model[] = [
  {
    model_id: 'claude-sonnet-5',
    name: 'Sonnet 5',
    agent_kind: 'claude',
    context_window: 1_000_000,
    thinking_modes: [],
  },
  {
    model_id: 'claude-sonnet-4-5',
    name: 'Sonnet 4.5',
    agent_kind: 'claude',
    context_window: 200_000,
    legacy: true,
    thinking_modes: [],
  },
];

const codexModels: Model[] = [
  {
    model_id: 'gpt-6-codex',
    name: 'GPT-6 Codex',
    agent_kind: 'codex',
    context_window: 400_000,
    thinking_modes: [],
  },
];
const agyModels: Model[] = [
  {
    model_id: 'gemini-4-pro',
    name: 'Gemini 4 Pro',
    agent_kind: 'antigravity',
    context_window: 1_000_000,
    thinking_modes: [],
  },
];

const choices = () =>
  within(screen.getByTestId('choices'))
    .queryAllByRole('button')
    .map((button) => button.textContent);

beforeEach(() => {
  models = claudeModels;
  profiles = [];
  modelsQuery.mockClear();
  useModelStore.setState({ modelByChat: {}, favoriteModelIds: [] });
});
afterEach(cleanup);

describe('legacy model choices', () => {
  it('hides legacy models unless selected or favorited', () => {
    const legacyId = claudeModels[1].model_id;
    render(<ModelSelector selectedModelId={claudeModels[0].model_id} onModelChange={vi.fn()} />);
    expect(screen.queryByRole('button', { name: legacyId })).toBeNull();

    cleanup();
    render(<ModelSelector selectedModelId={legacyId} onModelChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: legacyId })).toBeTruthy();

    cleanup();
    useModelStore.setState({ favoriteModelIds: [legacyId] });
    render(<ModelSelector selectedModelId={claudeModels[0].model_id} onModelChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: legacyId })).toBeTruthy();
  });
});

describe('enabled providers', () => {
  beforeEach(() => {
    models = [...claudeModels, ...codexModels, ...agyModels];
    profiles = [
      { id: 'p1', provider: 'claude' },
      { id: 'p2', provider: 'codex' },
    ];
  });

  it('offers models of providers with an account, one rail tab each', () => {
    render(
      <ModelSelector
        agentKind="enabled"
        selectedModelId="claude-sonnet-5"
        onModelChange={vi.fn()}
      />,
    );
    expect(modelsQuery).toHaveBeenCalledWith('all');
    const tabs = screen.getAllByRole('tab');
    expect(tabs.map((tab) => tab.getAttribute('aria-label'))).toEqual(['Claude Code', 'Codex']);
    // Opens on the selected model's provider.
    expect(screen.getByRole('tab', { name: 'Claude Code' }).getAttribute('aria-selected')).toBe(
      'true',
    );
    expect(choices()).toEqual(['claude-sonnet-5']);

    fireEvent.click(screen.getByRole('tab', { name: 'Codex' }));
    expect(choices()).toEqual(['gpt-6-codex']);
    expect(screen.queryByText('gemini-4-pro')).toBeNull();
  });

  it('puts a Favorites tab first once a model is starred', () => {
    useModelStore.setState({ favoriteModelIds: ['gpt-6-codex'] });
    render(
      <ModelSelector
        agentKind="enabled"
        selectedModelId="claude-sonnet-5"
        onModelChange={vi.fn()}
      />,
    );
    expect(screen.getAllByRole('tab').map((tab) => tab.getAttribute('aria-label'))).toEqual([
      'Favorites',
      'Claude Code',
      'Codex',
    ]);
    fireEvent.click(screen.getByRole('tab', { name: 'Favorites' }));
    expect(choices()).toEqual(['gpt-6-codex']);
  });

  it('has no rail with accounts of one provider', () => {
    profiles = [{ id: 'p2', provider: 'codex' }];
    render(
      <ModelSelector agentKind="enabled" selectedModelId="gpt-6-codex" onModelChange={vi.fn()} />,
    );
    expect(screen.queryByRole('tablist')).toBeNull();
    expect(choices()).toEqual(['gpt-6-codex']);
  });

  it('keeps to the thread provider without the rail', () => {
    render(
      <ModelSelector agentKind="codex" selectedModelId="gpt-6-codex" onModelChange={vi.fn()} />,
    );
    expect(modelsQuery).toHaveBeenCalledWith('codex');
    expect(screen.queryByRole('tablist')).toBeNull();
  });
});
