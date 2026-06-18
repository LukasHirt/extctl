import { defineConfig } from '@playwright/test'

export default defineConfig({
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:9731',
    trace: 'on-first-retry'
  },
  reporter: [['list']],
  retries: process.env.CI ? 2 : 0
})
