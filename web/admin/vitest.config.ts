import {mergeConfig, defineConfig} from 'vitest/config'
import viteConfig from './vite.config'

// The build config is reused rather than repeated, so the React plugin and any
// future alias stay in one place. Only the test environment is added: jsdom
// because these tests render real components and assert on focus.
export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.{ts,tsx}'],
    },
  }),
)
