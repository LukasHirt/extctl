import { defineConfig } from '@ownclouders/extension-sdk'

export default defineConfig({
  name: '{{EXT_ID}}',
  server: {
    port: {{VITE_PORT}},
  },
  build: {
    rollupOptions: {
      output: {
        entryFileNames: 'index.js',
      },
    },
  },
  test: {
    exclude: ['**/e2e/**'],
  },
})
