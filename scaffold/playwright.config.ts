import { defineConfig } from '@playwright/test'
import baseConfig from '../../playwright.config'

/**
 * See https://playwright.dev/docs/test-configuration.
 */
export default defineConfig({
  ...baseConfig,
  testDir: './tests/e2e',
  use: {
    ...baseConfig.use,
    // Trace mode is forced via `--trace` on the gate's own invocation
    // (gate/run-gate.sh) instead of here, so it doesn't ship in every
    // extension's committed config.
    screenshot: 'only-on-failure'
  }
})
