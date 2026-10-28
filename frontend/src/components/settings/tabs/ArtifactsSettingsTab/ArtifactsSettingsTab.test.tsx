// @vitest-environment happy-dom

import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { artifactService } from '@/services/artifactService';
import type { Artifact } from '@/types/artifact.types';
import { ArtifactsSettingsTab } from './ArtifactsSettingsTab';

vi.mock('@/services/artifactService', () => ({
  artifactService: { listArtifacts: vi.fn(), getShareUrl: vi.fn(), updateArtifact: vi.fn() },
}));

const artifact = (n: number): Artifact => ({
  id: `a${n}`,
  chat_id: null,
  title: `Page ${n}`,
  is_public: false,
  version: 1,
  pinned_at: null,
  created_at: '2026-10-25T00:00:00Z',
  updated_at: '2026-10-25T00:00:00Z',
});

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <MemoryRouter>{children}</MemoryRouter>
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.mocked(artifactService.listArtifacts).mockReset();
  vi.mocked(artifactService.updateArtifact).mockReset();
});

describe('ArtifactsSettingsTab', () => {
  it('loads 20 at a time until a short page', async () => {
    const list = vi.mocked(artifactService.listArtifacts);
    list.mockImplementation(async (params) =>
      params?.offset === 0
        ? Array.from({ length: 20 }, (_, i) => artifact(i))
        : [artifact(20), artifact(21)],
    );
    render(<ArtifactsSettingsTab />, { wrapper });

    await screen.findByText('Page 19');
    expect(list).toHaveBeenCalledWith({ q: undefined, limit: 20, offset: 0 });
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await screen.findByText('Page 21');
    expect(list).toHaveBeenLastCalledWith({ q: undefined, limit: 20, offset: 20 });
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('searches by title on the server', async () => {
    const list = vi.mocked(artifactService.listArtifacts);
    list.mockImplementation(async (params) => (params?.q ? [artifact(7)] : [artifact(1)]));
    render(<ArtifactsSettingsTab />, { wrapper });

    await screen.findByText('Page 1');
    fireEvent.change(screen.getByLabelText('Search artifacts'), { target: { value: ' Page 7 ' } });
    await screen.findByText('Page 7');
    await waitFor(() =>
      expect(list).toHaveBeenLastCalledWith({ q: 'Page 7', limit: 20, offset: 0 }),
    );
  });

  it('renames an artifact from its edit button', async () => {
    vi.mocked(artifactService.listArtifacts).mockResolvedValue([artifact(1)]);
    vi.mocked(artifactService.updateArtifact).mockResolvedValue({ ...artifact(1), title: 'Q3' });
    render(<ArtifactsSettingsTab />, { wrapper });

    await screen.findByText('Page 1');
    fireEvent.click(screen.getByRole('button', { name: 'Edit item' }));
    fireEvent.change(screen.getByLabelText('New name'), { target: { value: ' Q3 ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(artifactService.updateArtifact).toHaveBeenCalledWith('a1', { title: 'Q3' }),
    );
  });

  it('pins and unpins', async () => {
    vi.mocked(artifactService.listArtifacts).mockResolvedValue([
      artifact(1),
      { ...artifact(2), pinned_at: '2026-10-25T00:00:00Z' },
    ]);
    vi.mocked(artifactService.updateArtifact).mockResolvedValue(artifact(1));
    render(<ArtifactsSettingsTab />, { wrapper });

    await screen.findByText('Page 1');
    expect(screen.getByText('Pinned')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Pin to top' }));
    fireEvent.click(screen.getByRole('button', { name: 'Unpin' }));
    await waitFor(() => {
      expect(artifactService.updateArtifact).toHaveBeenCalledWith('a1', { pinned: true });
      expect(artifactService.updateArtifact).toHaveBeenCalledWith('a2', { pinned: false });
    });
  });
});
