import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Component tests run in jsdom. The suite is deliberately dependency-light: it
// exercises the form primitives in src/fields.tsx, which is where the console's
// numeric semantics live, rather than the whole shell.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    globals: false,
    restoreMocks: true,
  },
})
