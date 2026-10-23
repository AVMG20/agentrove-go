import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { PersistQueryClientProvider } from '@tanstack/react-query-persist-client';
// Global tokens/resets before component CSS so modules win cascade ties.
import './styles/app.scss';
import App from './App.tsx';
// Build-time palette CSS overrides (vite.config.ts).
import 'virtual:palette-overrides.css';
import { queryClient, persistOptions } from './lib/queryClient';
import { reloadStaleChunk } from './utils/lazyNamed';

window.addEventListener('vite:preloadError', (event) => {
  if (reloadStaleChunk()) {
    event.preventDefault();
  }
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <PersistQueryClientProvider client={queryClient} persistOptions={persistOptions}>
      <App />
    </PersistQueryClientProvider>
  </StrictMode>,
);
