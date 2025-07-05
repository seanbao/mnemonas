import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores([
    'dist',
    'dist-ssr',
    'coverage',
    'test-results',
    'playwright-report',
  ]),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
  },
  {
    files: ['src/pages/Files.tsx'],
    rules: {
      // FilesPage uses TanStack Virtual and opts out of React Compiler memoization explicitly.
      'react-hooks/incompatible-library': 'off',
    },
  },
  {
    files: ['src/**/*.tsx'],
    ignores: ['**/*.test.tsx', '**/test/**/*.{ts,tsx}', '**/__mocks__/**/*.{ts,tsx}'],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXOpeningElement[name.name='button']:not(:has(JSXAttribute[name.name='type']))",
          message: 'Native button elements must declare type="button", type="submit", or type="reset".',
        },
      ],
    },
  },
  // Disable react-refresh rules for test files and mocks
  {
    files: ['**/*.test.{ts,tsx}', '**/test/**/*.{ts,tsx}', '**/__mocks__/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])
