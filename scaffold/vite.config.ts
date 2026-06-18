import { defineConfig } from '@ownclouders/extension-sdk'

export default defineConfig({
  name: '{{EXT_ID}}',
  server: {
    port: 9731, // Increment this port number for each new extension.
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
