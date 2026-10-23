// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Model } from '@/types/chat.types';
import type { DropdownItemType } from '@/components/ui/primitives/Dropdown/Dropdown';
import { useModelStore } from '@/store/modelStore';
import { ModelSelector } from './ModelSelector';

vi.mock('@/hooks/useIsSplitMode', () => ({ useIsSplitMode: () => false }));
vi.mock('@/hooks/queries/useModelQueries', () => ({
  useModelsQuery: () => ({ data: models, isLoading: false }),
}));
vi.mock('@/components/ui/primitives/Dropdown/Dropdown', () => ({
  Dropdown: ({ items, value }: { items: DropdownItemType<Model>[]; value: Model }) => (
    <div>
      <span>{value.name}</span>
      <div data-testid="choices">
        {items.map(
          (item) =>
            item.type === 'item' && <button key={item.data.model_id}>{item.data.model_id}</button>,
        )}
      </div>
    </div>
  ),
}));

const models: Model[] = [
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

beforeEach(() => useModelStore.setState({ modelByChat: {}, favoriteModelIds: [] }));
afterEach(cleanup);

describe('legacy model choices', () => {
  it('hides legacy models unless selected or favorited', () => {
    const legacyId = models[1].model_id;
    render(<ModelSelector selectedModelId={models[0].model_id} onModelChange={vi.fn()} />);
    expect(screen.queryByRole('button', { name: legacyId })).toBeNull();

    cleanup();
    render(<ModelSelector selectedModelId={legacyId} onModelChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: legacyId })).toBeTruthy();

    cleanup();
    useModelStore.setState({ favoriteModelIds: [legacyId] });
    render(<ModelSelector selectedModelId={models[0].model_id} onModelChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: legacyId })).toBeTruthy();
  });
});
