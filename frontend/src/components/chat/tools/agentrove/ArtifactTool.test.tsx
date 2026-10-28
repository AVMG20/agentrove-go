// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useUIStore } from '@/store/uiStore';
import type { ToolAggregate } from '@/types/tools.types';
import { ArtifactTool } from './ArtifactTool';

function makeTool(over: Partial<ToolAggregate> = {}): ToolAggregate {
  return {
    id: 'tool-1',
    name: 'mcp__agentrove__create_artifact',
    title: 'create_artifact',
    status: 'completed',
    input: { title: 'Sales report', html: '<p>hi</p>' },
    result: [
      {
        type: 'text',
        text: JSON.stringify({
          artifact: {
            id: 'a1',
            title: 'Sales report',
            url: 'https://ai.example.com/artifacts/a1',
            is_public: false,
            version: 3,
            updated_at: '2026-10-22T10:00:00Z',
          },
        }),
      },
    ],
    children: [],
    ...over,
  };
}

const renderTool = (tool: ToolAggregate, chatId: string | undefined = 'c1') =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <ArtifactTool tool={tool} chatId={chatId} />
      </MemoryRouter>
    </QueryClientProvider>,
  );

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('ArtifactTool', () => {
  it('links the published artifact once the call completes', () => {
    renderTool(makeTool());

    expect(screen.getByText('Sales report')).toBeTruthy();
    expect(screen.getByText('Published artifact · v3')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Open in new tab' }).getAttribute('href')).toBe(
      '/artifacts/a1',
    );
  });

  it('opens the artifact beside the chat on a plain click', () => {
    const toggleArtifact = vi.fn();
    vi.spyOn(useUIStore, 'getState').mockReturnValue({
      ...useUIStore.getState(),
      toggleArtifact,
    });
    renderTool(makeTool());

    const card = screen.getByRole('link', { name: /Sales report/ });
    expect(card.getAttribute('href')).toBe('/artifacts/a1');
    fireEvent.click(card);
    expect(toggleArtifact).toHaveBeenCalledWith('c1', 'a1');
  });

  it('leaves a modified click to the browser', () => {
    const toggleArtifact = vi.fn();
    vi.spyOn(useUIStore, 'getState').mockReturnValue({
      ...useUIStore.getState(),
      toggleArtifact,
    });
    renderTool(makeTool());

    fireEvent.click(screen.getByRole('link', { name: /Sales report/ }), { metaKey: true });
    expect(toggleArtifact).not.toHaveBeenCalled();
  });

  it('labels updates as such', () => {
    renderTool(makeTool({ name: 'mcp__agentrove__update_artifact' }));

    expect(screen.getByText('Updated artifact · v3')).toBeTruthy();
  });

  it('falls back to the generic Bitbox line while running', () => {
    renderTool(makeTool({ status: 'started', result: undefined }));

    expect(screen.queryByRole('link', { name: 'Open in new tab' })).toBeNull();
    expect(screen.getByText('Bitbox · Create Artifact')).toBeTruthy();
  });
});
