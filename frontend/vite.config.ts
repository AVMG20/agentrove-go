import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';
import { generatePaletteCss, generateDiffPaletteCss } from './src/styles/generatePaletteCss';

// CSS generated from the hex palette source (src/styles/palettes.ts) at build time, so
// the RGB-channel vars aren't a second hand-maintained copy. Consumers import the virtual
// ids as a side-effect: main.tsx (surface tokens, global), DiffView (pierre diff tokens).
const VIRTUAL_CSS: Record<string, () => string> = {
  'virtual:palette-overrides.css': generatePaletteCss,
  'virtual:diff-palette-overrides.css': generateDiffPaletteCss,
};

function paletteCssPlugin(): Plugin {
  return {
    name: 'agentrove:palette-css',
    resolveId: (id) => (id in VIRTUAL_CSS ? `\0${id}` : null),
    load: (id) => (id.startsWith('\0') ? (VIRTUAL_CSS[id.slice(1)]?.() ?? null) : null),
  };
}

export default defineConfig({
  plugins: [react(), paletteCssPlugin()],
  css: {
    preprocessorOptions: {
      scss: {
        // sass-embedded via the modern API — the legacy JS API is deprecated
        api: 'modern-compiler',
      },
    },
  },
  worker: {
    // @pierre/diffs ships an imported worker entry, so builds need module
    // workers instead of Vite's default IIFE output.
    format: 'es',
  },
  optimizeDeps: {
    include: ['mermaid', '@tanstack/react-query'],
    esbuildOptions: {
      target: 'esnext',
    },
  },
  build: {
    target: 'esnext',
    commonjsOptions: {
      include: [/node_modules/],
      transformMixedEsModules: true,
    },
    rollupOptions: {
      output: {
        // Long-lived vendor chunks for caching. Everything else (Monaco, xterm,
        // mermaid, markdown, Shiki grammars) is split by its dynamic imports;
        // grouping those by hand pulled Vite's preload helper, and with it
        // Monaco's CSS, into the initial load.
        manualChunks(id) {
          if (!id.includes('/node_modules/')) return undefined;
          if (
            /\/node_modules\/(react|react-dom|react-router|react-router-dom|scheduler)\//.test(id)
          ) {
            return 'react-vendor';
          }
          if (id.includes('/node_modules/@tanstack/')) return 'query-vendor';
          return undefined;
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    host: true,
    port: 3000,
    proxy: {
      '/api': {
        target: process.env.API_PROXY_TARGET || 'http://api:8080',
        changeOrigin: true,
        ws: true,
        secure: false,
      },
    },
  },
});
