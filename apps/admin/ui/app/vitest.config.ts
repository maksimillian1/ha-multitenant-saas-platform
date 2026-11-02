import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config.ts'

// The SPA's tests render the real router and pages in jsdom; the API is a
// fetch stub per test (src/test/api.ts).
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      setupFiles: ['./src/test/setup.ts'],
      unstubGlobals: true,
      restoreMocks: true,
    },
  }),
)
