import tsParser from '@typescript-eslint/parser'
import tsPlugin from '@typescript-eslint/eslint-plugin'
import vueParser from 'vue-eslint-parser'
import pluginVue from 'eslint-plugin-vue'

/** @type {import('eslint').Linter.Config[]} */
export default [
  {
    ignores: ['dist/**', 'node_modules/**']
  },
  // TypeScript source files
  {
    files: ['src/**/*.ts'],
    plugins: { '@typescript-eslint': tsPlugin },
    languageOptions: {
      parser: tsParser,
      parserOptions: { sourceType: 'module', ecmaVersion: 2020 }
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'warn'
    }
  },
  // Vue single-file components
  {
    files: ['src/**/*.vue'],
    plugins: { vue: pluginVue, '@typescript-eslint': tsPlugin },
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        parser: tsParser,
        sourceType: 'module',
        ecmaVersion: 2020
      }
    },
    rules: {
      ...pluginVue.configs['vue3-essential'].rules,
      'vue/multi-word-component-names': 'off',
      'vue/require-default-prop': 'off',
      '@typescript-eslint/no-explicit-any': 'error',
      'no-console': 'warn'
    }
  }
]
