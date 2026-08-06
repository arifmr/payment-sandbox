import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config'

/**
 * Test config is separate from vite.config.ts because `defineConfig` from 'vite' has no
 * `test` key — putting it there type-checks only if the Vitest types happen to augment
 * Vite's, which is fragile. Merging keeps the alias and plugin setup in one place.
 */
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      globals: true,
      environment: 'jsdom',
      // jsdom disables storage and some APIs on an opaque origin, so give it a real URL.
      environmentOptions: { jsdom: { url: 'http://localhost:5173' } },
      setupFiles: ['./src/test/setup.ts'],
      css: false,
      coverage: {
        provider: 'v8',
        include: ['src/**/*.{ts,tsx}'],
        exclude: ['src/**/*.test.{ts,tsx}', 'src/test/**', 'src/main.tsx', 'src/vite-env.d.ts'],
      },
    },
  }),
)
