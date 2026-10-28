import { availableParallelism } from 'node:os';
import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config';

// Merge the app's vite config so the `@/` alias resolves without a second copy.
// Pure-logic tests stay in node; component tests opt into a DOM with Vitest's
// per-file environment directive (happy-dom; jsdom where a test needs it).
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'node',
      include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
      // Stylesheets load as empty strings in tests; this one is read (as ?raw) to
      // check it agrees with utils/device.ts.
      css: { include: [/_responsive\.scss/] },
      // Threads start faster than forked processes. Use every CPU the box
      // allows: Vitest's default leaves one idle, which is half of a 2-CPU box.
      pool: 'vmThreads',
      maxWorkers: availableParallelism(),
    },
  }),
);
