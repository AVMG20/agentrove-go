import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  // SCSS compiles through sass-embedded's modern compiler API — Vite 7's only mode, so
  // no `css.preprocessorOptions.scss.api` setting is needed.
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
