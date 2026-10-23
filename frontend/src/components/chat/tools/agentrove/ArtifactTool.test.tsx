// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
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

const renderTool = (tool: ToolAggregate) =>
  render(
    <MemoryRouter>
      <ArtifactTool tool={tool} />
    </MemoryRouter>,
  );

afterEach(cleanup);

describe('ArtifactTool', () => {
  it('links the published artifact once the call completes', () => {
    renderTool(makeTool());

    expect(screen.getByText('Sales report')).toBeTruthy();
    expect(screen.getByText('Published artifact · v3')).toBeTruthy();
    expect(screen.getByRole('link', { name: /open/i }).getAttribute('href')).toBe('/artifacts/a1');
  });

  it('labels updates as such', () => {
    renderTool(makeTool({ name: 'mcp__agentrove__update_artifact' }));

    expect(screen.getByText('Updated artifact · v3')).toBeTruthy();
  });

  it('falls back to the generic AgentRove line while running', () => {
    renderTool(makeTool({ status: 'started', result: undefined }));

    expect(screen.queryByRole('link', { name: /open/i })).toBeNull();
    expect(screen.getByText('AgentRove · Create Artifact')).toBeTruthy();
  });
});
